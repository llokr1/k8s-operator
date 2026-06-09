# 03. Operator SDK 시작하기 (프로젝트 스캐폴딩 & 개발 루프)

### 개요

`2`에서 배운 Client-go를 바탕으로 동작하는 Operator SDK 프레임워크가 어떤 구조로 구성되어 있고, 어떻게 동작하는지 알아보고자 한다.

- controller-runtime, Kubebuilder 등의 다른 프레임워크를 통해 Operator SDK의 구조를 알아본다.
- **Operator SDK로 프로젝트를 생성**한다.
- CRD를 직접 구현해본다.

---

## controller-runtime

`2`에서도 언급했듯이, client-go 라이브러리의 추상화를 통해 Controller를 쉽게 만들도록 제공해주는 라이브러리이다.

Kubebuilder가 이를 활용한다고 볼 수 있다.

```
client-go 개념                    controller-runtime 컴포넌트
──────────────                  ──────────────────────────
SharedIndexInformer         →    Cache
Indexer                     →    Cache
Workqueue                   →    Controller 내부
EventHandler                →    Predicate → Reconciler
Reconcile 함수               →    Reconciler
```

## Kubebuilder

CRD와 Custom Controller 프로젝트를 쉽게 만들 수 있도록 제공하는 프레임워크 및 CLI 도구라고 볼 수 있다.

```bash
# 새 프로젝트 생성
kubebuilder init --domain my.domain --repo my.domain/guestbook
# 새로운 API(group/version) 생성
kubebuilder create api --group apps --version v1alpha1 --kind PracticeApp
# manifests 파일 생성 - (CRD 수정 후) .yaml 파일에 반영
make manifests
```

### 마커

`kubebuilder`와 `controller-gen`에서 정의한 문법으로, **특정 형식의 주석을 읽어** 코드나 YAML을 자동으로 생성한다.

```go
// +kubebuilder:object:root=true
// 이 struct를 CRD로 등록하라

// +kubebuilder:subresource:status
// Status를 서브리소스로 분리하라

// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// kubectl get 시 출력할 컬럼 추가

// +kubebuilder:validation:Minimum=1
// 필드 값 최솟값 검증
```

## CRD 정의

`practice` 프로젝트의 `api/v1alpha1/practiceapp_types.go`를 정의한다.

```go
// api/v1alpha1/practiceapp_types.go

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

// PracticeApp is the Schema for the practiceapps API.
type PracticeApp struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`

    // spec defines the desired state of PracticeApp
    // +required
    Spec PracticeAppSpec `json:"spec"`

    // status defines the observed state of PracticeApp
    // +optional
    Status PracticeAppStatus `json:"status,omitempty"`
}
```

`Reconcile`에서 간단한 로그 출력과 Status를 업데이트 하는 로직을 구현한다.

```go
// internal/controller/practiceapp_controller.go

func (r *PracticeAppReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    log := logf.FromContext(ctx)

    // 1. 캐시에서 현재 상태 조회
    app := &appsv1alpha1.PracticeApp{}
    if err := r.Get(ctx, req.NamespacedName, app); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    // 2. Spec 로그 출력
    log.Info("Reconciling PracticeApp",
        "name", app.Name,
        "image", app.Spec.Image,
        "replicas", app.Spec.Replicas,
        "port", app.Spec.Port,
    )

    // 3. Status 업데이트
    phase := "Running"
    message := fmt.Sprintf("%s %d개 실행 중 (port: %d)", app.Spec.Image, app.Spec.Replicas, app.Spec.Port)

    // 4. 현재 Status와 다를 때만 업데이트 (멱등성)
    if app.Status.Phase == phase {
        log.Info("Status 변경 없음, Reconcile 종료", "phase", phase)
        return ctrl.Result{}, nil
    }

    app.Status.Phase = phase
    app.Status.Message = message

    if err := r.Status().Update(ctx, app); err != nil {
        log.Error(err, "Status 업데이트 실패")
        return ctrl.Result{}, err
    }

    log.Info("Status 업데이트 완료",
        "phase", app.Status.Phase,
        "message", app.Status.Message,
    )

    return ctrl.Result{}, nil
}
```

### Go struct → CRD YAML 변환 과정

```go
// api/v1alpha1/practiceapp_types.go
type PracticeAppSpec struct {
    Image    string `json:"image"`    // string 타입
    Replicas int32  `json:"replicas"` // integer 타입
    Port     int32  `json:"port"`     // integer 타입
}
```
```yaml
# CRD YAML (자동 생성)
spec:
  properties:
    image:
      type: string      # Go string → string
    replicas:
      type: integer     # Go int32 → integer
      format: int32     # 32비트 정수임을 명시
    port:
      type: integer     # Go int32 → integer
      format: int32
  required:
  - image               # omitempty 없는 필드만 required
  - replicas
  - port
```

### CRD 등록

`make manifests` 명령어를 통해 다음 명령을 통해 Go언어로 선언한 CRD의 yaml 파일을 생성한다.

`controller-gen`을 통해 `api/v1alpha1/{project}_types.go`의 마커와 `struct` 필드를 읽어서 CRD, RBAC yaml을 자동으로 생성한다.

```bash
.PHONY: manifests
manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	"$(CONTROLLER_GEN)" rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases
```

### CRD의 기본 구조

생성된 CRD의 yaml 파일의 기본 구조는 아래와 같다.

`practice/config/crd/bases/apps.my.domain_practiceapps.yaml`

```yaml
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: practiceapps.apps.my.domain  # {plural}.{group} 형식

spec:
  group: apps.my.domain   # API 그룹 (URL 경로에 사용)

  names:
    kind: PracticeApp
    plural: practiceapps
    singular: practiceapp
    listKind: PracticeAppList

  scope: Namespaced          # 리소스 적용 범위 : Namespaced / Cluster 둘 중 하나

  versions:
  - name: v1alpha1           # API 버전
    served: true             # 이 버전으로 요청 처리 여부
    storage: true            # etcd 저장 버전 (하나만 true)

    schema:
      openAPIV3Schema:
        properties:
          spec:
            properties:
              image:         # 필드 이름 (json 태그에서 옴)
                type: string # 타입 (string/integer/boolean/array/object)
              replicas:
                type: integer
                format: int32
              port:
                type: integer
                format: int32
            required:        # 필수 필드 목록
            - image
            - replicas
            - port

    subresources:
      status: {}             # Status 서브리소스 (r.Status().Update() 사용 시 필수)
```

그리고, `make install` 명령어로 생성된 yaml 파일을 통해 API Server에 배포한다.

Makefile에서 `install` 명령어는 다음과 같이 구성되어있다.

```bash
.PHONY: install
install: manifests kustomize ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	@out="$$( "$(KUSTOMIZE)" build config/crd 2>/dev/null || true )"; \
	if [ -n "$$out" ]; then echo "$$out" | "$(KUBECTL)" apply -f -; else echo "No CRDs to install; skipping."; fi
```

`kustomize build config/crd | kubectl apply -f -`를 통해 **config 파일에 정의된 클러스터에 CRD를 설치**한다.

마지막으로 `make run` 명령어로 manifests, generate, fmt, vet 명령어가 실행되고, `./cmd/main.go` 파일이 실행되어 Controller가 실행된다.

`generate`을 통해 DeepCopy 메소드가 생성되고, `fmt`를 통해 포맷 규칙을 맞추고, `vet`을 통해 코드를 검사한다.

> `DeepCopy` 메소드란 캐시의 오브젝트를 수정할 때, 직접적으로 수정하면 API Server와의 정합성이 깨지기 때문에 오브젝트를 복사하여 생성하는 메소드이다.

```bash
.PHONY: run
run: manifests generate fmt vet ## Run a controller from your host.
	go run ./cmd/main.go
```

위 명령어들을 통해 CRD 및 Controller를 실행시키고, 리소스를 관리하며 로그를 분석한다.

- 리소스 최초 생성 : `kubectl apply -f config/samples/apps_v1alpha1_practiceapp.yaml`

    리소스가 생성되면서 `ADDED` 이벤트가 발생한다.
    
    1. ADDED 이벤트 → Workqueue → Reconcile 실행
    2. `r.Get()` → PracticeApp 조회
       - spec.image    = "nginx:latest"
       - spec.replicas = 2
       - spec.port     = 80
       - status.phase  = "" (아직 Status가 없음)
       phase 결정: `"Running"`
    3. r.Status().Update() 실행  ← Status 업데이트
       - `app.Status.Phase("") != phase("Running")`
    4. "Status 업데이트 완료" 로그 출력
    
    → ADDED의 Reconcile이 `r.Status().Update()`를 호출한다.
    
    → 리소스가 수정되는 `MODIFIED` 이벤트가 발생하여 두 번째 Reconcile이 수행된다.
    
    → 이때, `app.Status.Phase("Running") == phase("Running")`으로 Reconcile은 그냥 종료되어 멱등성이 성립된다.

- 리소스 조회 : `kubectl get practiceapps`

    아래에 선언한 마커의 출력 형식이 그대로 출력된다.

```go
// +kubebuilder:printcolumn:name="Image",type=string,JSONPath=`.spec.image`
// +kubebuilder:printcolumn:name="Replicas",type=integer,JSONPath=`.spec.replicas`
// +kubebuilder:printcolumn:name="Port",type=integer,JSONPath=`.spec.port`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
```

- 리소스 수정 : `kubectl edit practiceapps`

    파일에서 리소스를 수정한다.
    
    spec의 `replicas: 3`, `image: nginx:1.25` 등으로 변경하면 phase는 여전히 Running이지만 message가 갱신된다.
        
    기존의 Status와 달라진 경우 `r.Status().Update()` 가 호출되고, 마찬가지로 Reconcile이 다시 실행된다.

## Operator SDK

Operator SDK는 Kubebuilder를 내부적으로 포함하고, 그 위에 부가적인 기능을 추가한 프레임워크이다.

따라서 아래의 명령어로 프로젝트를 생성하는 경우, 전반적인 프로젝트의 구조는 Kubebuilder와 매우 유사하기 때문에 내부적으로 CRD를 생성하거나 Reconcile을 구현하는 방식도 유사하다.

```bash
mkdir -p operator
cd operator
operator-sdk init --domain my.domain --project-name k8s-operator --owner llokr --repo github.com/llokr1/k8s-operator/operator
```

하지만 Kubebuilder를 사용하는 것과 다르게, Helm이나 Ansible을 활용하여 Operator를 배포하는 것이 가능하고, OLM도 지원한다.

**OLM**이란, Operator를 하나의 패키지 및 쿠버네티스 리소스로 관리하여 설치 / 업그레이드 / 삭제를 관리하는 시스템이다.

---

**참고 자료**

https://book.kubebuilder.io/quick-start.html