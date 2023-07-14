package main

import (
	"context"
	goflag "flag"
	"os"

	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/status"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/component-base/cli"
	utilflag "k8s.io/component-base/cli/flag"

	"github.com/openshift/cluster-olm-operator/pkg/clients"
	"github.com/openshift/cluster-olm-operator/pkg/controller"
	"github.com/openshift/cluster-olm-operator/pkg/version"
)

func main() {
	pflag.CommandLine.SetNormalizeFunc(utilflag.WordSepNormalizeFunc)
	pflag.CommandLine.AddGoFlagSet(goflag.CommandLine)

	command := newRootCommand()
	code := cli.Run(command)
	os.Exit(code)
}

func newRootCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster-olm-operator",
		Short: "OpenShift Cluster OLM Operator",
	}
	cmd.AddCommand(newStartCommand())
	return cmd
}

func newStartCommand() *cobra.Command {
	cmd := controllercmd.NewControllerCommandConfig(
		"cluster-olm-operator",
		version.Get(),
		runOperator,
	).NewCommandWithContext(context.Background())
	cmd.Use = "start"
	cmd.Short = "Start the Cluster OLM Operator"
	return cmd
}

func runOperator(ctx context.Context, cc *controllercmd.ControllerContext) error {
	cl, err := clients.New(cc)
	if err != nil {
		return err
	}

	cb := controller.Builder{
		Assets:            os.DirFS("/operand-assets"),
		Clients:           cl,
		ControllerContext: cc,
	}

	controllers, relatedObjects, err := cb.BuildControllers("catalogd", "operator-controller", "rukpak")
	if err != nil {
		return err
	}

	namespaces := sets.New[string]()
	for _, obj := range relatedObjects {
		namespaces.Insert(obj.Namespace)
	}

	cl.KubeInformersForNamespaces = v1helpers.NewKubeInformersForNamespaces(cl.KubeClient, namespaces.UnsortedList()...)

	controllerNames := make([]string, 0, len(controllers))
	controllerList := make([]factory.Controller, 0, len(controllers))

	for name, controller := range controllers {
		controllerNames = append(controllerNames, name)
		controllerList = append(controllerList, controller)
	}

	upgradeableConditionController := controller.NewStaticUpgradeableConditionController(
		"OLMStaticUpgradeableConditionController",
		cl.OperatorClient,
		cc.EventRecorder.ForComponent("OLMStaticUpgradeableConditionController"),
		controllerNames,
	)

	versionGetter := status.NewVersionGetter()
	versionGetter.SetVersion("operator", status.VersionForOperatorFromEnv())

	clusterOperatorController := status.NewClusterOperatorStatusController(
		"olm",
		relatedObjects,
		cl.ConfigClient.ConfigV1(),
		cl.ConfigInformerFactory.Config().V1().ClusterOperators(),
		cl.OperatorClient,
		versionGetter,
		cc.EventRecorder.ForComponent("olm"),
	)

	cl.StartInformers(ctx)

	for _, c := range append(controllerList, upgradeableConditionController, clusterOperatorController) {
		go func(c factory.Controller) {
			defer runtime.HandleCrash()
			c.Run(ctx, 1)
		}(c)
	}

	<-ctx.Done()
	return nil
}
