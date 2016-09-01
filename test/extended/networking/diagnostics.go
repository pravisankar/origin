package networking

import (
	exutil "github.com/openshift/origin/test/extended/util"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("[networking] oadm diagnostics NetworkCheck", func() {
	It("should successfully validate pod to node, pod to pod, pod to service and pod to external network connectivity", func() {
		oc := exutil.NewCLI("diagnostics", exutil.KubeConfigPath()).AsAdmin()
		Expect(networkCheck(oc)).To(Succeed())
	})
})

func networkCheck(oc *exutil.CLI) error {
	return oc.Run("adm").Args("diagnostics", "NetworkCheck").Execute()
}
