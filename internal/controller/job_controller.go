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

package controller

import (
	"context"
	"fmt"
	"reflect"

	apps "k8s.io/api/apps/v1"
	batch "k8s.io/api/batch/v1"
	core "k8s.io/api/core/v1"
	net "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	jobicov1 "github.com/andrescosta/jobicok8s/api/v1"
	"github.com/andrescosta/jobicok8s/internal/ref"
)

// JobReconciler reconciles a Job object
type JobReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=jobico.coeux.dev,resources=jobs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=jobico.coeux.dev,resources=jobs/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=jobico.coeux.dev,resources=jobs/finalizers,verbs=update

//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=create;update;patch
//+kubebuilder:rbac:groups="",resources=services,verbs=create;update;patch
//+kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=create;update;patch
//+kubebuilder:rbac:groups=batch,resources=jobs,verbs=create;update;patch

func (r *JobReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	var jobdef jobicov1.Job
	if err := r.Get(ctx, req.NamespacedName, &jobdef); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	fmt.Printf("creating %s", jobdef.Name)
	for _, e := range jobdef.Spec.Events {
		if err := r.reconcileDeployment(ctx, e, jobdef); err != nil {
			fmt.Printf("%v\n", err)
			return ctrl.Result{}, err
		}

		// Service
		if err := r.reconcileService(ctx, e, jobdef); err != nil {
			fmt.Printf("%v\n", err)
			return ctrl.Result{}, err
		}

		// Ingress
		if err := r.reconcileIngress(ctx, e, jobdef); err != nil {
			fmt.Printf("%v\n", err)
			return ctrl.Result{}, err
		}

		// Job
		if e, err := r.reconcileJob(ctx, e, jobdef); err != nil || e {
			if err != nil {
				fmt.Printf("%v\n", err)
				return ctrl.Result{}, err
			} else {
				return ctrl.Result{
					Requeue: true,
				}, nil
			}
		}
	}

	return ctrl.Result{}, nil
}

// Comp
// Exist?
// No o err => return
// Yes
// Delete old
// Create new one

func (r *JobReconciler) reconcileJob(ctx context.Context, evt jobicov1.Event, jobdef jobicov1.Job) (bool, error) {
	jobName := "exec-" + evt.Name
	orig := new(batch.Job)
	current := r.getJobDefinition(jobName, jobdef, evt)
	exist, err := r.get(ctx, orig, jobName, jobdef.Namespace)
	if err != nil {
		return false, err
	}
	if exist {
		if orig.DeletionTimestamp != nil {
			return true, nil
		}
		b := reflect.DeepEqual(current.Spec.Template.Spec.Containers[0].Env, orig.Spec.Template.Spec.Containers[0].Env)
		if b {
			return false, nil
		}
		// the Job's template section is immutable and cannot be updated.
		// We delete it and recreate it below.
		err := r.Delete(ctx, orig)
		if err != nil {
			return false, err
		}
		return true, nil
	}
	if err := ctrl.SetControllerReference(&jobdef, current, r.Scheme); err != nil {
		return false, err
	}
	if err := r.Create(ctx, current); err != nil {
		return false, err
	}
	return false, nil
}

func (*JobReconciler) getJobDefinition(jobName string, jobdef jobicov1.Job, evt jobicov1.Event) *batch.Job {
	job := batch.Job{
		ObjectMeta: v1.ObjectMeta{
			Name:      jobName,
			Namespace: jobdef.Namespace,
		},
		Spec: batch.JobSpec{
			Template: core.PodTemplateSpec{
				ObjectMeta: v1.ObjectMeta{
					Labels: map[string]string{"app": "exec", "event": evt.Name},
				},
				Spec: core.PodSpec{
					RestartPolicy: core.RestartPolicyOnFailure,
					Volumes: []core.Volume{
						{
							Name: "local-persistent-storage",
							VolumeSource: core.VolumeSource{
								PersistentVolumeClaim: &core.PersistentVolumeClaimVolumeSource{
									ClaimName: "test-pvc",
								},
							},
						},
					},
					Containers: []core.Container{
						{
							Name:            "exec-" + evt.Name,
							Image:           "exec:v1",
							ImagePullPolicy: core.PullNever,
							Env: []core.EnvVar{
								{
									Name:  "event",
									Value: evt.Name,
								},
								{
									Name:  "NATS_URL",
									Value: "nats://queue:4222",
								},
								{
									Name:  "wasm",
									Value: evt.Wasm,
								},
								{
									Name:  "dir",
									Value: "/mnt/exec",
								},
							},
							VolumeMounts: []core.VolumeMount{
								{
									Name:      "local-persistent-storage",
									MountPath: "/mnt/exec",
								},
							},
						},
					},
				},
			},
		},
	}
	return &job
}

func (r *JobReconciler) reconcileIngress(ctx context.Context, e jobicov1.Event, jobdef jobicov1.Job) error {
	ingressName := "listener-ingress-http-" + e.Name
	orig := new(net.Ingress)
	current := r.getIngressDefinition(ingressName, jobdef, e)
	if err := ctrl.SetControllerReference(&jobdef, current, r.Scheme); err != nil {
		fmt.Printf("error: %v\n", err)
		return err
	}
	exist, err := r.get(ctx, orig, ingressName, jobdef.Namespace)
	if err != nil {
		return err
	}
	if exist {
		oev := orig.ObjectMeta.Annotations["event"]
		cev := current.ObjectMeta.Annotations["event"]
		if cev != oev {
			return r.Patch(ctx, current, client.StrategicMergeFrom(orig))
		}
		return nil
	}
	if err := r.Create(ctx, current); err != nil {
		fmt.Printf("error: %v\n", err)
		return err
	}
	return nil
}

func (*JobReconciler) getIngressDefinition(ingressName string, jobdef jobicov1.Job, e jobicov1.Event) *net.Ingress {
	ingress := net.Ingress{
		ObjectMeta: v1.ObjectMeta{
			Name:      ingressName,
			Namespace: jobdef.Namespace,
			Annotations: map[string]string{
				"nginx.ingress.kubernetes.io/ssl-redirect": "true",
				"event": e.Name,
			},
		},
		Spec: net.IngressSpec{
			IngressClassName: ref.Of("nginx"),
			Rules: []net.IngressRule{
				{
					Host: "listener",
					IngressRuleValue: net.IngressRuleValue{
						HTTP: &net.HTTPIngressRuleValue{
							Paths: []net.HTTPIngressPath{
								{
									Path:     "/listener/" + e.Name,
									PathType: ref.Of(net.PathType("Prefix")),
									Backend: net.IngressBackend{
										Service: &net.IngressServiceBackend{
											Name: "listener-" + e.Name,
											Port: net.ServiceBackendPort{
												Number: 8080,
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	return &ingress
}

func (r *JobReconciler) reconcileService(ctx context.Context, e jobicov1.Event, jobdef jobicov1.Job) error {
	serviceName := "service-" + e.Name
	current := r.getServiceDefinition(serviceName, jobdef, e)
	if err := ctrl.SetControllerReference(&jobdef, current, r.Scheme); err != nil {
		fmt.Printf("error: %v\n", err)
		return err
	}
	orig := new(core.Service)
	exist, err := r.get(ctx, orig, serviceName, jobdef.Namespace)
	if err != nil {
		return err
	}
	if exist {
		evc := current.Labels["event"]
		evo := current.Labels["event"]
		if evc != evo {
			return r.Patch(ctx, orig, client.StrategicMergeFrom(current))
		}
		return nil
	}
	if err := r.Create(ctx, current); err != nil {
		fmt.Printf("error: %v\n", err)
		return err
	}
	return nil
}

func (*JobReconciler) getServiceDefinition(serviceName string, jobdef jobicov1.Job, e jobicov1.Event) *core.Service {
	service := core.Service{
		ObjectMeta: v1.ObjectMeta{
			Name:      serviceName,
			Namespace: jobdef.Namespace,
			Labels:    map[string]string{"app": "listener", "event": e.Name},
		},
		Spec: core.ServiceSpec{
			Selector: map[string]string{"app": "listener", "event": e.Name},
			Ports:    []core.ServicePort{{Port: 8080, TargetPort: intstr.FromInt(8080)}},
			Type:     core.ServiceTypeClusterIP,
		},
	}
	return &service
}

func (r *JobReconciler) reconcileDeployment(ctx context.Context, e jobicov1.Event, jobdef jobicov1.Job) error {
	deploymentName := "deployment-" + e.Name
	orig := new(apps.Deployment)
	current := r.getDeploymentDefinition(deploymentName, jobdef, e)
	if err := ctrl.SetControllerReference(&jobdef, current, r.Scheme); err != nil {
		fmt.Printf("error: %v\n", err)
		return err
	}
	exist, err := r.get(ctx, orig, deploymentName, jobdef.Namespace)
	if err != nil {
		return err
	}
	if exist {
		b := reflect.DeepEqual(orig.Spec.Template.Spec.Containers[0].Env, orig.Spec.Template.Spec.Containers[0].Env)
		if !b {
			return r.Patch(ctx, current, client.StrategicMergeFrom(orig))
		}
		return nil
	}
	if err := r.Create(ctx, current); err != nil {
		return err
	}
	return nil
}

func (*JobReconciler) getDeploymentDefinition(deploymentName string, jobdef jobicov1.Job, e jobicov1.Event) *apps.Deployment {
	deployment := apps.Deployment{
		ObjectMeta: v1.ObjectMeta{
			Name:      deploymentName,
			Namespace: jobdef.Namespace,
		},
		Spec: apps.DeploymentSpec{
			Replicas: ref.Of(int32(1)),
			Selector: &v1.LabelSelector{
				MatchLabels: map[string]string{"app": "listener", "event": e.Name},
			},
			Template: core.PodTemplateSpec{
				ObjectMeta: v1.ObjectMeta{
					Labels: map[string]string{"app": "listener", "event": e.Name},
				},
				Spec: core.PodSpec{
					RestartPolicy: core.RestartPolicyAlways,
					Volumes: []core.Volume{
						{
							Name: "schema",
							VolumeSource: core.VolumeSource{
								ConfigMap: &core.ConfigMapVolumeSource{
									DefaultMode: ref.Of(int32(420)),
									LocalObjectReference: core.LocalObjectReference{
										Name: e.Schema.Key,
									},
								},
							},
						},
					},
					Containers: []core.Container{
						{
							Name:            "listener-" + e.Name,
							Image:           "listener:v1",
							ImagePullPolicy: core.PullNever,
							Ports: []core.ContainerPort{
								{
									ContainerPort: 8080,
								},
							},
							Env: []core.EnvVar{
								{
									Name:  "event",
									Value: e.Name,
								},
								{
									Name:  "NATS_URL",
									Value: "nats://queue:4222",
								},
								{
									Name:  "schema",
									Value: e.Schema.Key + ".json",
								},
							},
							VolumeMounts: []core.VolumeMount{
								{
									Name:      "schema",
									MountPath: "/etc/listener",
								},
							},
						},
					},
				},
			},
		},
	}
	return &deployment
}

// SetupWithManager sets up the controller with the Manager.
func (r *JobReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&jobicov1.Job{}).
		Complete(r)
}

func (r *JobReconciler) get(ctx context.Context, o client.Object, name, namespace string) (bool, error) {
	nn := types.NamespacedName{
		Namespace: namespace,
		Name:      name,
	}
	err := r.Get(ctx, nn, o)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
