# 05. Operator 고도화 — Status & Finalizer

### 개요

`03`에서 Deployment와 Service를 생성하고, `status.phase`에 `"Running"` 문자열을 기록하는 오퍼레이터를 완성했다.

그런데 실제로 운영하다 보면 이런 의문이 생긴다.

> `phase: Running`인데, Deployment의 Pod가 아직 Pending 상태라면?  
> Controller가 Deployment는 잘 만들었지만, Service 포트가 충돌 나서 실패했다면?  
> 사용자가 `kubectl get practiceapp`을 쳤을 때 **지금 실제로 어떤 상태인지** 알 수 있을까?

→ 단순 문자열 하나로는 **여러 상태가 동시에 존재하는 상황을 표현할 수 없다.**

또 다른 의문도 생긴다.

> CR을 삭제했을 때 OwnerReference GC가 Deployment와 Service를 지워주는 건 알겠다.  
> 그런데 Controller가 외부 시스템(AWS, DB 등)에 리소스를 만들었다면?  
> 또는 **Pod가 완전히 종료된 뒤에 삭제되어야 하는 순서**가 있다면?

→ Kubernetes GC는 **클러스터 외부 리소스나 삭제 순서를 보장하지 못한다.**

이번 주차에서는 이 두 가지 한계를 해결하는 패턴을 학습한다.

- **Conditions** — 구조화된 Status 표현으로 복잡한 상태를 정확하게 기록
- **Finalizer** — 삭제 전 정리 작업을 Controller가 직접 제어

---

## Status 업데이트 전략

### Phase string의 한계

`03`에서 사용한 방식은 아래와 같다.

```go
// 03의 방식 — 문자열 하나
app.Status.Phase = "Running"
app.Status.Message = "nginx 2개 실행 중 (port: 80)"
```

이 방식의 문제점은 아래와 같다.

| 문제 | 상황 |
|---|---|
| 상태가 하나 | Deployment는 OK, Service는 문제 → 표현 불가 |
| 언제 바뀌었는지 모름 | 디버깅 시 시간 정보 없음 |
| Spec이 바뀌었는지 모름 | Status가 최신 Spec을 반영한 건지 알 수 없음 |
| 이유를 알 수 없음 | Running인데 왜 Running인지 표현 불가 |

---

### Conditions 패턴

Kubernetes 공식 리소스(Deployment, Node 등)도 Status를 표현할 때 `Conditions` 배열을 사용한다.

```bash
# Deployment의 실제 Status Conditions
kubectl describe deployment my-app
# Conditions:
#   Type           Status  Reason
#   ----           ------  ------
#   Progressing    True    NewReplicaSetAvailable
#   Available      True    MinimumReplicasAvailable
```

`metav1.Condition` 구조체는 아래와 같다.

```go
// k8s.io/apimachinery/pkg/apis/meta/v1
type Condition struct {
    Type               string             // 상태 이름 (예: "Available", "Progressing")
    Status             ConditionStatus    // "True" / "False" / "Unknown"
    ObservedGeneration int64              // 이 Condition이 반영한 Spec의 generation
    LastTransitionTime metav1.Time        // 마지막으로 Status가 바뀐 시각
    Reason             string             // 상태의 원인 (PascalCase, 예: "DeploymentReady")
    Message            string             // 사람이 읽는 설명
}
```

---

### PracticeAppStatus에 Conditions 추가

`api/v1alpha1/practiceapp_types.go`를 아래와 같이 수정한다.

```go
// PracticeAppStatus defines the observed state of PracticeApp
type PracticeAppStatus struct {
    // conditions: 각 상태를 독립적으로 기록
    // +optional
    // +listType=map
    // +listMapKey=type
    Conditions []metav1.Condition `json:"conditions,omitempty"`

    // observedGeneration: 이 Status가 반영한 Spec의 generation
    // +optional
    ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}
```

> `ObservedGeneration`은 Spec이 변경될 때마다 1씩 증가하는 `metadata.generation`을 Status에 복사하는 필드다.  
> 이를 통해 **Status가 현재 Spec을 반영한 것인지, 오래된 것인지**를 판단할 수 있다.

```
metadata.generation: 3   ← Spec이 3번 바뀜
status.observedGeneration: 2  ← Status는 2번째 Spec까지만 반영됨
                               → Reconcile이 아직 실행 중임을 알 수 있음
```

Condition 타입 상수도 함께 선언한다.

```go
const (
    // ConditionAvailable: Deployment와 Service가 정상적으로 생성된 상태
    ConditionAvailable = "Available"

    // ConditionProgressing: 리소스 생성/업데이트가 진행 중인 상태
    ConditionProgressing = "Progressing"

    // ConditionDegraded: 오류가 발생한 상태
    ConditionDegraded = "Degraded"
)
```

---

### apimeta.SetStatusCondition으로 updateStatus 재구현

`apimeta.SetStatusCondition`은 Conditions 배열을 멱등하게 관리해주는 헬퍼다.  
같은 `Type`의 Condition이 이미 있으면 업데이트하고, 없으면 추가한다.

```go
import (
    apimeta "k8s.io/apimachinery/pkg/api/meta"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (r *PracticeAppReconciler) updateStatus(ctx context.Context, app *appsv1alpha1.PracticeApp) error {
    log := logf.FromContext(ctx)

    // Available Condition 설정
    apimeta.SetStatusCondition(&app.Status.Conditions, metav1.Condition{
        Type:               ConditionAvailable,
        Status:             metav1.ConditionTrue,
        ObservedGeneration: app.Generation,    // 현재 Spec의 generation 기록
        Reason:             "DeploymentReady",
        Message:            fmt.Sprintf("%s %d개 실행 중 (port: %d)", app.Spec.Image, app.Spec.Replicas, app.Spec.Port),
    })

    // ObservedGeneration 갱신
    app.Status.ObservedGeneration = app.Generation

    log.Info("Status 업데이트", "generation", app.Generation)
    return r.Status().Update(ctx, app)
}
```

```bash
# 결과
kubectl get practiceapp practiceapp-sample -o yaml
# status:
#   observedGeneration: 2
#   conditions:
#   - type: Available
#     status: "True"
#     reason: DeploymentReady
#     message: "nginx:latest 2개 실행 중 (port: 80)"
#     lastTransitionTime: "2026-06-03T10:00:00Z"
#     observedGeneration: 2
```

오류가 발생했을 때는 `Degraded` Condition을 기록한다.

```go
// reconcileDeployment에서 오류 발생 시
apimeta.SetStatusCondition(&app.Status.Conditions, metav1.Condition{
    Type:               ConditionDegraded,
    Status:             metav1.ConditionTrue,
    ObservedGeneration: app.Generation,
    Reason:             "DeploymentFailed",
    Message:            err.Error(),
})
r.Status().Update(ctx, app)
return err
```

> `Phase` 문자열과 달리 `Available: True`와 `Degraded: False`가 **동시에 독립적으로 존재**할 수 있기 때문에, 복잡한 상태를 정확하게 표현할 수 있다.

---

## Finalizer 패턴

### OwnerReference GC의 한계

`03`에서 `SetControllerReference`를 통해 CR이 삭제되면 Deployment와 Service도 자동으로 삭제된다고 배웠다.

그런데 아래 상황에서는 OwnerReference GC가 동작하지 않는다.

| 상황 | 이유 |
|---|---|
| 외부 리소스 정리 | AWS S3, 외부 DB 등은 Kubernetes 오브젝트가 아님 |
| 다른 Namespace의 리소스 | OwnerReference는 동일 Namespace 내에서만 작동 |
| Cluster-scoped 리소스 | Namespaced 리소스가 Cluster-scoped 리소스를 소유 불가 |
| 삭제 순서 보장 | "Pod가 모두 종료된 뒤에 삭제" 같은 순서 제어 불가 |

→ 이런 경우 **Finalizer**를 사용해서 Controller가 직접 정리 작업을 수행한 뒤 삭제를 허용한다.

---

### Finalizer 동작 원리

```
일반 삭제 (Finalizer 없음):
kubectl delete → API Server → 즉시 etcd에서 제거

Finalizer가 있는 경우:
kubectl delete → API Server → DeletionTimestamp 설정 (실제 삭제 보류)
                            → Controller가 DeletionTimestamp 감지
                            → 정리 작업 수행 (외부 리소스 삭제 등)
                            → Finalizer 제거
                            → Finalizer 목록이 비면 → etcd에서 실제 삭제
```

Finalizer는 리소스의 `metadata.finalizers` 배열에 문자열로 기록된다.

```yaml
metadata:
  name: practiceapp-sample
  finalizers:
    - practiceapp.apps.my.domain/finalizer  # 이 값이 있는 한 삭제되지 않음
  deletionTimestamp: "2026-06-03T10:00:00Z" # kubectl delete 이후 설정됨
```

---

### Reconcile에 Finalizer 패턴 추가

`internal/controller/practiceapp_controller.go`를 수정한다.

```go
const finalizerName = "practiceapp.apps.my.domain/finalizer"

func (r *PracticeAppReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    log := logf.FromContext(ctx)

    app := &appsv1alpha1.PracticeApp{}
    if err := r.Get(ctx, req.NamespacedName, app); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    // ① DeletionTimestamp가 설정됐으면 → 정리 작업 수행
    //    kubectl delete가 호출된 상태 — 실제 삭제는 Finalizer가 제거될 때까지 보류됨
    if !app.DeletionTimestamp.IsZero() {
        return ctrl.Result{}, r.handleDeletion(ctx, app)
    }

    // ② Finalizer가 없으면 등록
    //    최초 생성 시 1회만 실행됨
    if !controllerutil.ContainsFinalizer(app, finalizerName) {
        controllerutil.AddFinalizer(app, finalizerName)
        return ctrl.Result{}, r.Update(ctx, app)
        // Update 후 MODIFIED 이벤트 → Reconcile 재실행됨
    }

    // ③ 정상 Reconcile 로직 (기존과 동일)
    if err := r.reconcileDeployment(ctx, app); err != nil {
        return ctrl.Result{}, err
    }
    if err := r.reconcileService(ctx, app); err != nil {
        return ctrl.Result{}, err
    }

    return ctrl.Result{}, r.updateStatus(ctx, app)
}
```

---

### handleDeletion — 정리 작업 구현

```go
func (r *PracticeAppReconciler) handleDeletion(ctx context.Context, app *appsv1alpha1.PracticeApp) error {
    log := logf.FromContext(ctx)

    // Finalizer가 없으면 이미 정리 완료 — 바로 종료
    if !controllerutil.ContainsFinalizer(app, finalizerName) {
        return nil
    }

    // 정리 작업 수행
    // (외부 리소스 삭제, 알림 발송, 로그 기록 등 원하는 작업을 여기에 구현)
    log.Info("PracticeApp 삭제 전 정리 작업 수행", "name", app.Name)

    // 예: 외부 시스템에 삭제 요청
    // if err := r.cleanupExternalResources(ctx, app); err != nil {
    //     return err  // 실패 시 Finalizer를 제거하지 않음 → 재시도
    // }

    // 정리 완료 → Finalizer 제거 → 실제 삭제 허용
    controllerutil.RemoveFinalizer(app, finalizerName)
    if err := r.Update(ctx, app); err != nil {
        return err
    }

    log.Info("Finalizer 제거 완료 — 리소스 삭제 진행", "name", app.Name)
    return nil
}
```

> 정리 작업 중 오류가 발생하면 `RemoveFinalizer`를 호출하지 않고 `error`를 반환한다.  
> Reconcile이 재시도되면서 정리 작업이 다시 실행된다. 이것이 **Finalizer 패턴의 멱등성**이다.

---

### 전체 삭제 흐름

```
kubectl delete practiceapp practiceapp-sample
    │
    ▼
API Server: DeletionTimestamp 설정 (즉시 삭제 안 됨)
    │
    ▼
MODIFIED 이벤트 → Workqueue → Reconcile 실행
    │
    ▼
DeletionTimestamp.IsZero() == false → handleDeletion() 호출
    │
    ├── 정리 작업 수행 (외부 리소스 삭제 등)
    │
    ├── RemoveFinalizer() → r.Update()
    │
    ▼
finalizers: [] (빈 배열)
    │
    ▼
API Server: Finalizer 없음 확인 → etcd에서 실제 삭제
    │
    ▼
OwnerReference GC: Deployment, Service 자동 삭제
```

---

### OwnerReference GC vs Finalizer 비교

| 항목 | OwnerReference GC | Finalizer |
|---|---|---|
| 동작 주체 | Kubernetes GC (자동) | Controller (직접 제어) |
| 삭제 대상 | 같은 Namespace의 Kubernetes 리소스 | 제한 없음 (외부 포함) |
| 삭제 순서 보장 | 보장 안 됨 | 보장 가능 |
| 구현 필요 여부 | 불필요 | 직접 구현 필요 |
| 적합한 경우 | Deployment, Service 등 자식 리소스 | 외부 리소스, 순서 보장, Cluster-scoped 리소스 |

> 두 패턴은 **서로 대체 관계가 아니라 역할 분리 관계**다.  
> Deployment·Service처럼 같은 Namespace에 속한 자식 리소스는 OwnerReference GC에 맡기고,  
> 그 외 정리가 필요한 작업은 Finalizer로 처리한다.

---

### Deletion Policy — Foreground / Background / Orphan

`kubectl delete`에 `--cascade` 옵션으로 삭제 방식을 선택할 수 있다.

```bash
# Background (기본값): 부모 먼저 삭제 → GC가 자식 삭제
kubectl delete practiceapp practiceapp-sample

# Foreground: 자식이 모두 삭제된 후 부모 삭제
kubectl delete practiceapp practiceapp-sample --cascade=foreground

# Orphan: 자식 리소스는 남겨두고 부모만 삭제
kubectl delete practiceapp practiceapp-sample --cascade=orphan
```

```
Background (기본):          Foreground:                 Orphan:
CR 삭제                     CR에 foreground 마커 설정    CR 삭제
  ↓                           ↓                            ↓
Deployment 남음              Deployment 삭제 대기          Deployment 유지
  ↓ (GC가 나중에 삭제)         ↓                            (독립 리소스로 존재)
Deployment 삭제              CR 삭제
```

---

## 전체 Reconcile 흐름 (4주차 → 5주차 변화)

```go
func (r *PracticeAppReconciler) Reconcile(...) (ctrl.Result, error) {
    // [공통] CR 조회
    app := ...
    r.Get(ctx, req.NamespacedName, app)

    // [5주차 추가] ① 삭제 요청 처리
    if !app.DeletionTimestamp.IsZero() {
        return handleDeletion(ctx, app)    // 정리 → Finalizer 제거
    }

    // [5주차 추가] ② Finalizer 등록
    if !ContainsFinalizer(app, finalizerName) {
        AddFinalizer → r.Update()
    }

    // [4주차와 동일] ③ 리소스 생성/업데이트
    reconcileDeployment(ctx, app)
    reconcileService(ctx, app)

    // [5주차 변경] ④ Status 업데이트
    // Phase 문자열 → Conditions + ObservedGeneration
    updateStatus(ctx, app)
}
```

---

**참고 자료**

- https://book.kubebuilder.io/reference/using-finalizers
- https://kubernetes.io/docs/concepts/architecture/garbage-collection/
- https://kubernetes.io/docs/concepts/workloads/controllers/garbage-collection/#foreground-deletion
- https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/controller/controllerutil
- https://pkg.go.dev/k8s.io/apimachinery/pkg/api/meta#SetStatusCondition
- https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties
