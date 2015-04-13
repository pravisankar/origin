package start

import (

	// Admission control plug-ins used by OpenShift
	_ "github.com/GoogleCloudPlatform/kubernetes/plugin/pkg/admission/admit"
	_ "github.com/GoogleCloudPlatform/kubernetes/plugin/pkg/admission/limitranger"
	_ "github.com/GoogleCloudPlatform/kubernetes/plugin/pkg/admission/namespace/exists"
	_ "github.com/GoogleCloudPlatform/kubernetes/plugin/pkg/admission/namespace/lifecycle"
	_ "github.com/GoogleCloudPlatform/kubernetes/plugin/pkg/admission/resourcequota"
	_ "github.com/openshift/origin/pkg/pod/admission"
	_ "github.com/openshift/origin/pkg/project/admission"

	// Scheduler plug-ins used by OpenShift
	_ "github.com/openshift/origin/pkg/scheduler"
)
