/*
Copyright 2026 llokr.

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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	appsv1alpha1 "github.com/llokr1/k8s-operator/practice/api/v1alpha1"
)

// PracticeAppReconciler reconciles a PracticeApp object
type PracticeAppReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=apps.my.domain,resources=practiceapps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps.my.domain,resources=practiceapps/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps.my.domain,resources=practiceapps/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete

func (r *PracticeAppReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// 1. PracticeApp 조회 (캐시에서)
	app := &appsv1alpha1.PracticeApp{}
	if err := r.Get(ctx, req.NamespacedName, app); err != nil {
		// 리소스가 삭제된 경우 → 정상 종료
		// OwnerReference로 인해 자식 리소스(Deployment, Service)도 자동 삭제됨
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	log.Info("Reconciling PracticeApp",
		"name", app.Name,
		"image", app.Spec.Image,
		"replicas", app.Spec.Replicas,
		"port", app.Spec.Port,
	)

	// 2. Deployment 생성 또는 업데이트
	if err := r.reconcileDeployment(ctx, app); err != nil {
		return ctrl.Result{}, err
	}

	// 3. Service 생성 또는 업데이트
	if err := r.reconcileService(ctx, app); err != nil {
		return ctrl.Result{}, err
	}

	// 4. Status 업데이트
	return ctrl.Result{}, r.updateStatus(ctx, app)
}

// reconcileDeployment: Deployment가 없으면 생성, 있으면 원하는 상태와 비교 후 업데이트
func (r *PracticeAppReconciler) reconcileDeployment(ctx context.Context, app *appsv1alpha1.PracticeApp) error {
	log := logf.FromContext(ctx)

	desired := buildDeployment(app)

	// OwnerReference 설정: PracticeApp이 삭제되면 Deployment도 자동 삭제
	if err := controllerutil.SetControllerReference(app, desired, r.Scheme); err != nil {
		return err
	}

	// 기존 Deployment 조회
	existing := &appsv1.Deployment{}
	err := r.Get(ctx, client.ObjectKey{Name: app.Name, Namespace: app.Namespace}, existing)

	if errors.IsNotFound(err) {
		// 없으면 생성
		log.Info("Deployment 생성", "name", desired.Name)
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	// 있으면 원하는 상태와 비교 후 업데이트
	if *existing.Spec.Replicas != app.Spec.Replicas ||
		existing.Spec.Template.Spec.Containers[0].Image != app.Spec.Image {
		existing.Spec.Replicas = &app.Spec.Replicas
		existing.Spec.Template.Spec.Containers[0].Image = app.Spec.Image
		log.Info("Deployment 업데이트", "name", existing.Name)
		return r.Update(ctx, existing)
	}

	log.Info("Deployment 변경 없음", "name", existing.Name)
	return nil
}

// reconcileService: Service가 없으면 생성, 있으면 원하는 상태와 비교 후 업데이트
func (r *PracticeAppReconciler) reconcileService(ctx context.Context, app *appsv1alpha1.PracticeApp) error {
	log := logf.FromContext(ctx)

	desired := buildService(app)

	// OwnerReference 설정: PracticeApp이 삭제되면 Service도 자동 삭제
	if err := controllerutil.SetControllerReference(app, desired, r.Scheme); err != nil {
		return err
	}

	// 기존 Service 조회
	existing := &corev1.Service{}
	err := r.Get(ctx, client.ObjectKey{Name: app.Name, Namespace: app.Namespace}, existing)

	if errors.IsNotFound(err) {
		// 없으면 생성
		log.Info("Service 생성", "name", desired.Name)
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	// 있으면 포트 비교 후 업데이트
	if existing.Spec.Ports[0].Port != app.Spec.Port {
		existing.Spec.Ports[0].Port = app.Spec.Port
		existing.Spec.Ports[0].TargetPort = intstr.FromInt32(app.Spec.Port)
		log.Info("Service 업데이트", "name", existing.Name)
		return r.Update(ctx, existing)
	}

	log.Info("Service 변경 없음", "name", existing.Name)
	return nil
}

// updateStatus: 현재 상태와 다를 때만 Status 업데이트 (멱등성)
func (r *PracticeAppReconciler) updateStatus(ctx context.Context, app *appsv1alpha1.PracticeApp) error {
	log := logf.FromContext(ctx)

	phase := "Running"
	message := fmt.Sprintf("%s %d개 실행 중 (port: %d)", app.Spec.Image, app.Spec.Replicas, app.Spec.Port)

	if app.Status.Phase == phase {
		return nil
	}

	app.Status.Phase = phase
	app.Status.Message = message

	log.Info("Status 업데이트", "phase", phase, "message", message)
	return r.Status().Update(ctx, app)
}

// buildDeployment: PracticeApp spec을 기반으로 원하는 Deployment 오브젝트 생성
func buildDeployment(app *appsv1alpha1.PracticeApp) *appsv1.Deployment {
	labels := map[string]string{"app": app.Name}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      app.Name,
			Namespace: app.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &app.Spec.Replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "app",
							Image: app.Spec.Image,
							Ports: []corev1.ContainerPort{
								{ContainerPort: app.Spec.Port},
							},
						},
					},
				},
			},
		},
	}
}

// buildService: PracticeApp spec을 기반으로 원하는 Service 오브젝트 생성
func buildService(app *appsv1alpha1.PracticeApp) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      app.Name,
			Namespace: app.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": app.Name},
			Ports: []corev1.ServicePort{
				{
					Port:       app.Spec.Port,
					TargetPort: intstr.FromInt32(app.Spec.Port),
				},
			},
		},
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *PracticeAppReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&appsv1alpha1.PracticeApp{}).
		// Deployment나 Service가 변경되면 PracticeApp Reconcile 재실행
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Named("practiceapp").
		Complete(r)
}
