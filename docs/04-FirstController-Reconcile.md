# 04. CRD + Controller(Reconcile)

## 배경

`02`로 프로젝트 생성과 배포 루프는 준비됐지만, 아직 오퍼레이터가 관리할 도메인 로직(무엇을 감시하고 어떤 리소스를 생성/유지할지)이 없다. 

따라서 CRD(`spec/status`)와 Controller(Reconcile)를 연결해 "오퍼레이터가 실제로 동작하는 최소 단위"를 완성한다.

## 학습 목표

- **CRD 스키마**로 원하는 상태(`spec`)와 관찰 상태(`status`)를 설계한다.
- Reconcile로 **실제 리소스 생성/업데이트를 멱등하게 구현**한다.
- **상태(`status`)를 반영**해 사용자가 현재 상황을 확인할 수 있게 한다.

---

## CRD 정의

`practice` 프로젝트의 `api/v1alpha1/practiceapp_types.go`를 수정한다.

```go
// PracticeAppSpec defines the desired state of PracticeApp
type PracticeAppSpec struct {
    // image: 실행할 컨테이너 이미지
    Image string `json:"image"`

    // replicas: 실행할 Pod 수
    Replicas int32 `json:"replicas"`

    // port: 컨테이너가 노출하는 포트 번호
    Port int32 `json:"port"`
}

// PracticeAppStatus defines the observed state of PracticeApp
type PracticeAppStatus struct {
    // phase: Controller가 판단한 현재 상태
    // +optional
    Phase string `json:"phase,omitempty"`

    // message: 현재 상태에 대한 설명
    // +optional
    Message string `json:"message,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Image",type=string,JSONPath=`.spec.image`
// +kubebuilder:printcolumn:name="Replicas",type=integer,JSONPath=`.spec.replicas`
// +kubebuilder:printcolumn:name="Port",type=integer,JSONPath=`.spec.port`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
```

`spec`에는 이미지, 복제본 수, 포트 세 가지 필드만 선언하고, `status`에는 Controller가 계산한 `phase`와 `message`를 기록한다.

> Spec은 사용자가 선언한 **원하는 상태**, Status는 Controller가 관찰한 **현재 상태**이다. 두 영역은 반드시 분리해야 한다.

---

## Reconcile 함수 구조

`internal/controller/practiceapp_controller.go`를 수정한다.

```go
func (r *PracticeAppReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    log := logf.FromContext(ctx)

    // 1. 캐시에서 PracticeApp 조회
    //    리소스가 삭제된 경우 IgnoreNotFound로 정상 종료
    //    OwnerReference로 자식 리소스(Deployment, Service)도 자동 삭제됨
    app := &appsv1alpha1.PracticeApp{}
    if err := r.Get(ctx, req.NamespacedName, app); err != nil {
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
```

각 단계를 별도 함수로 분리하여 관심사를 나눈다.

Reconcile은 **조율자 역할**만 하고, 실제 처리 로직은 `reconcileDeployment`, `reconcileService`, `updateStatus`가 담당한다.

---

## 리소스 생성/업데이트 — reconcileDeployment

```go
func (r *PracticeAppReconciler) reconcileDeployment(ctx context.Context, app *appsv1alpha1.PracticeApp) error {
    log := logf.FromContext(ctx)

    desired := buildDeployment(app) // Spec 기반으로 원하는 Deployment 생성

    // OwnerReference 설정: PracticeApp이 삭제되면 Deployment도 자동으로 삭제됨
    if err := controllerutil.SetControllerReference(app, desired, r.Scheme); err != nil {
        return err
    }

    // 기존 Deployment 조회 (캐시 사용)
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

    // 있으면 원하는 상태와 현재 상태를 비교 후 업데이트 (선언형 비교)
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
```

`Get → 없으면 Create, 있으면 비교 후 Update` 패턴이 핵심이다. **이벤트 종류(ADDED/MODIFIED)를 분기하지 않고**, 현재 상태와 원하는 상태를 비교하는 방식이기 때문에 Resync나 재시도 상황에서도 항상 올바르게 동작한다. (멱등성)

`reconcileService`도 동일한 패턴으로 구현한다. 포트가 변경된 경우에만 `r.Update()`를 호출한다.

---

## OwnerReference

```go
controllerutil.SetControllerReference(app, desired, r.Scheme)
```

`SetControllerReference`는 자식 리소스(`desired`)의 `OwnerReferences` 필드에 부모 리소스(`app`)의 정보를 기록한다.

```yaml
# Deployment에 자동으로 추가되는 OwnerReference
ownerReferences:
  - apiVersion: apps.my.domain/v1alpha1
    kind: PracticeApp
    name: practiceapp-sample
    uid: ...
    controller: true
    blockOwnerDeletion: true
```

이를 통해 **부모 CR이 삭제되면 Kubernetes GC가 자식 Deployment와 Service를 자동으로 삭제**한다. Controller가 직접 삭제 로직을 구현할 필요가 없다.

---

## 리소스 빌더 함수 — buildDeployment / buildService

Reconcile 함수에서 "원하는 상태"를 표현하는 오브젝트를 만드는 역할을 분리한다.

```go
func buildDeployment(app *appsv1alpha1.PracticeApp) *appsv1.Deployment {
    labels := map[string]string{"app": app.Name}

    return &appsv1.Deployment{
        ObjectMeta: metav1.ObjectMeta{
            Name:      app.Name,
            Namespace: app.Namespace,
        },
        Spec: appsv1.DeploymentSpec{
            Replicas: &app.Spec.Replicas,
            Selector: &metav1.LabelSelector{MatchLabels: labels},
            Template: corev1.PodTemplateSpec{
                ObjectMeta: metav1.ObjectMeta{Labels: labels},
                Spec: corev1.PodSpec{
                    Containers: []corev1.Container{
                        {
                            Name:  "app",
                            Image: app.Spec.Image, // CR Spec의 image 필드
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
                    Port:       app.Spec.Port, // CR Spec의 port 필드
                    TargetPort: intstr.FromInt32(app.Spec.Port),
                },
            },
        },
    }
}
```

빌더 함수를 분리하면 `reconcileDeployment`는 **생성/업데이트 판단 로직**에만 집중하고, **원하는 상태의 조립 로직**은 `buildDeployment`가 전담한다.

---

## Status 업데이트 — updateStatus

```go
func (r *PracticeAppReconciler) updateStatus(ctx context.Context, app *appsv1alpha1.PracticeApp) error {
    log := logf.FromContext(ctx)

    phase := "Running"
    message := fmt.Sprintf("%s %d개 실행 중 (port: %d)", app.Spec.Image, app.Spec.Replicas, app.Spec.Port)

    // 현재 Status와 동일하면 업데이트 생략 (멱등성)
    if app.Status.Phase == phase {
        return nil
    }

    app.Status.Phase = phase
    app.Status.Message = message

    log.Info("Status 업데이트", "phase", phase, "message", message)
    return r.Status().Update(ctx, app) // Status 서브리소스 API 호출
}
```

`r.Update()` 가 아닌 `r.Status().Update()`를 사용해야 하는 이유는, Status가 서브리소스로 분리되어 있기 때문이다. `r.Update()`로는 Status 변경이 무시된다.

---

## SetupWithManager — Owns()

```go
func (r *PracticeAppReconciler) SetupWithManager(mgr ctrl.Manager) error {
    return ctrl.NewControllerManagedBy(mgr).
        For(&appsv1alpha1.PracticeApp{}).  // PracticeApp CR 변경 감지
        Owns(&appsv1.Deployment{}).        // Deployment 변경 시 부모 CR Reconcile 재실행
        Owns(&corev1.Service{}).           // Service 변경 시 부모 CR Reconcile 재실행
        Named("practiceapp").
        Complete(r)
}
```

`Owns()`는 **OwnerReference가 설정된 자식 리소스의 변경을 감지**하여, 해당 부모 CR의 Reconcile을 자동으로 재실행한다.

예를 들어, 누군가 Deployment를 직접 수정하거나 삭제하더라도 Reconcile이 즉시 실행되어 원하는 상태로 복구된다. 이것이 **자기치유(Self-healing)** 동작의 핵심이다.

```
Deployment 삭제
    → Owns() 감지
    → PracticeApp Reconcile 재실행
    → reconcileDeployment: IsNotFound → r.Create()
    → Deployment 복구 완료
```

---

## 실행 흐름

```bash
# 1. CRD 스키마 생성 및 클러스터에 등록
make manifests && make install

# 2. 컨트롤러 로컬 실행
make run

# 3. CR 배포 (별도 터미널)
kubectl apply -f config/samples/apps_v1alpha1_practiceapp.yaml
```

```yaml
# config/samples/apps_v1alpha1_practiceapp.yaml
apiVersion: apps.my.domain/v1alpha1
kind: PracticeApp
metadata:
  name: practiceapp-sample
spec:
  image: nginx:latest
  replicas: 2
  port: 80
```

CR을 배포하면 아래 순서로 Reconcile이 실행된다.

1. `ADDED` 이벤트 → Workqueue → Reconcile 실행
2. `r.Get()` → PracticeApp 조회
3. `reconcileDeployment` → Deployment가 없으므로 `r.Create()`
4. `reconcileService` → Service가 없으므로 `r.Create()`
5. `updateStatus` → `phase("")` ≠ `"Running"` → `r.Status().Update()`
6. Status 변경으로 `MODIFIED` 이벤트 발생 → 두 번째 Reconcile 실행
7. Deployment·Service 변경 없음 → 변경 없음 로그 출력
8. `updateStatus` → `phase("Running")` == `"Running"` → 업데이트 생략, 종료

```bash
# 결과 확인
kubectl get practiceapp
# NAME                  IMAGE          REPLICAS   PORT   PHASE
# practiceapp-sample    nginx:latest   2          80     Running

kubectl get deployment,service
# deployment.apps/practiceapp-sample   2/2   2   2   ...
# service/practiceapp-sample           ClusterIP  ...   80/TCP
```
