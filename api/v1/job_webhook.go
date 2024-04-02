/*
Copyright 2024.

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

	net "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	validationutils "k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// log is for logging in this package.
var joblog = logf.Log.WithName("job-resource")

// SetupWebhookWithManager will setup the manager to manage the webhooks
func (r *Job) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

//+kubebuilder:webhook:path=/mutate-jobico-coeux-dev-v1-job,mutating=true,failurePolicy=fail,sideEffects=None,groups=jobico.coeux.dev,resources=jobs,verbs=create;update,versions=v1,name=mjob.kb.io,admissionReviewVersions=v1

var _ webhook.Defaulter = &Job{}

// Default implements webhook.Defaulter so a webhook will be registered for the type
func (r *Job) Default() {
	joblog.Info("default", "name", r.Name)
	// No defaults
}

// TODO(user): change verbs to "verbs=create;update;delete" if you want to enable deletion validation.
//+kubebuilder:webhook:path=/validate-jobico-coeux-dev-v1-job,mutating=false,failurePolicy=fail,sideEffects=None,groups=jobico.coeux.dev,resources=jobs,verbs=create;update,versions=v1,name=vjob.kb.io,admissionReviewVersions=v1

var _ webhook.Validator = &Job{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (r *Job) ValidateCreate() (admission.Warnings, error) {
	joblog.Info("validate create", "name", r.Name)
	return r.validate()
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *Job) ValidateUpdate(old runtime.Object) (admission.Warnings, error) {
	joblog.Info("validate update", "name", r.Name)
	return r.validate()
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *Job) ValidateDelete() (admission.Warnings, error) {
	joblog.Info("validate delete", "name", r.Name)
	// TODO:
	// The queue is empty
	return nil, nil
}

func (r *Job) validate() (admission.Warnings, error) {
	allErrors := r.validateJob()
	for _, e := range r.Spec.Events {
		allErrors = append(allErrors, r.validateEvent(e)...)
		err := r.validateIfEventExists(e)
		if err != nil {
			allErrors = append(allErrors, err)
		}
	}

	if len(allErrors) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: GroupVersion.Group, Kind: r.Kind},
			r.Name, allErrors)
	}
	return nil, nil
}

func (r *Job) validateJob() field.ErrorList {
	var allErrors field.ErrorList
	if len(r.Spec.Events) == 0 {
		allErrors = append(allErrors, field.Required(field.NewPath("events[]"), ""))
	}
	if len(r.ObjectMeta.Name) > validationutils.DNS1035LabelMaxLength-24 {
		allErrors = append(allErrors, field.Invalid(field.NewPath("metadata").Child("name"), r.Name, "must be no more than 35 characters"))
	}
	return allErrors
}

func (*Job) validateEvent(e Event) field.ErrorList {
	var allErrors field.ErrorList
	if e.Name == "" {
		allErrors = append(allErrors, field.Required(field.NewPath("events[]").Child("name"), ""))
	}
	if e.Wasm == "" {
		allErrors = append(allErrors, field.Required(field.NewPath("events[]").Child("wasm"), ""))
	}
	if e.Schema.Key == "" {
		allErrors = append(allErrors, field.Required(field.NewPath("events[]").Child("key"), ""))
	}
	return allErrors
}

func (r *Job) validateIfEventExists(evt Event) *field.Error {
	expr := fmt.Sprintf("event=%s, owner!=%s", evt.Name, r.Name)
	labelSelector, err := labels.Parse(expr)
	if err != nil {
		return field.InternalError(field.NewPath("events[]").Child("name"), err)
	}
	opts := &client.ListOptions{LabelSelector: labelSelector}

	igs := net.IngressList{}
	if err := r.List(context.Background(), &igs, opts); err != nil {
		return field.InternalError(field.NewPath("events[]").Child("name"), err)
	}
	if len(igs.Items) > 0 {
		return field.Invalid(field.NewPath("events[]").Child("name"), evt.Name, "A listener already exists for the event.")
	}
	return nil
}
