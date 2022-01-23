/*
Copyright 2021.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1

import (
	"context"
	"fmt"
	"os"
	"strings"

	v1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	authv1 "k8s.io/client-go/kubernetes/typed/authentication/v1"
	"k8s.io/client-go/rest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// log is for logging in this package.
var iplog = logf.Log.WithName("ip-resource")

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!

// TODO(user): change verbs to "verbs=create;update;delete" if you want to enable deletion validation.
//+kubebuilder:webhook:path=/validate-paas-org-v1-ip,mutating=false,failurePolicy=fail,sideEffects=None,groups=paas.org,resources=ips,verbs=create;update;delete,versions=v1,name=vip.kb.io,admissionReviewVersions=v1

func getControllerServiceAccount() string {
	myConfig, err := rest.InClusterConfig()
	if err != nil {
		fmt.Printf("an error occurred while creating an in-cluster config: %v\n", err)
		os.Exit(1)
	}
	authClient, err := authv1.NewForConfig(myConfig)
	if err != nil {
		fmt.Printf("an error occurred while creating an authentication client: %v\n", err)
		os.Exit(1)
	}
	tokenReview, err := authClient.TokenReviews().Create(context.Background(), &v1.TokenReview{Spec: v1.TokenReviewSpec{Token: myConfig.BearerToken}}, metav1.CreateOptions{})
	if err != nil {
		fmt.Printf("an error occurred while creating a token review: %v\n", err)
		os.Exit(1)
	}
	return tokenReview.Status.User.Username
}

var controllerServiceAccount = getControllerServiceAccount()

// implement admission handler
type IPValidationHandler struct {
	decoder *admission.Decoder
}

func (i *IPValidationHandler) Handle(ctx context.Context, req admission.Request) admission.Response {
	operation := strings.ToLower(string(req.Operation))
	iplog.Info(fmt.Sprintf("user %[1]s operation %[2]s", req.UserInfo.Username, operation))
	if req.UserInfo.Username != controllerServiceAccount {
		return admission.Denied(fmt.Sprintf("user %[1]s cannot %[2]s ip objects, only %[3]s is allowed", req.UserInfo.Username, operation, controllerServiceAccount))
	}
	return admission.Allowed("")
}

func (i *IPValidationHandler) InjectDecoder(d *admission.Decoder) error {
	i.decoder = d
	return nil
}
