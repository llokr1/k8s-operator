# 02. Client-go 패키지

### 개요

오퍼레이터 패턴을 구현하기 전, `client-go` 라이브러리를 통해 쿠버네티스가 **현재 상태를 선언형 상태로 변경하는 과정**을 알아본다.

`kubectl`, Operator SDK 모두 내부적으로 `client-go` 라이브러리를 활용하여 쿠버네티스 리소스에 접근하고, 추후 애플리케이션 레벨에서 쿠버네티스 리소스를 제어하고자 할 때도 `client-go` 패키지를 활용할 수 있다.

```
client-go              ← Watch, Informer, Workqueue, Reconcile
↑
controller-runtime
↑
Operator SDK           
```

`client-go` 패키지는 Controller 구현에 필요한 핵심 기능을 제공한다.
- 리소스 변경 감지(Watch)
- 이벤트 기반 처리(Informer)
- 로컬 캐시(Lister)
- 비동기 작업 큐(Workqueue)

따라서 위 핵심 컴포넌트를 학습하여 **Controller 패턴의 동작 과정을** 이해하고, 이를 바탕으로 **Operator SDK를 통해서 오퍼레이터를 구현하는 방향성을 갖추는 것**이 목표이다.

---

## Controller 동작 과정

`client-go` 의 소스 코드를 통해 Controller가 현재 상태를 선언된 상태로 맞춰가는 과정은 다음과 같다.

```
API Server
    │
    │ ① List & Watch
    ▼
Reflector → 변경 이벤트를 감지   # tools/cache/reflector.go
    │
    │ ② 변경 이벤트를 Delta로 변환 (Add/Update/Delete 이벤트)
    ▼
DeltaFIFO → 변경 이벤트 저장    # tools/cache/delta_fifo.go
    │
    │ ③ Pop → handleDeltas
    ▼
┌───────────────────────────────────────┐
│        SharedIndexInformer            │  # tools/cache/shared_informer.go
│                                       │
│    Indexer ←─ 캐시 상태 업데이트          │  # tools/cache/store.go
│       │                               │
│  EventHandler 호출                     │
└───────────┬───────────────────────────┘
            │ ④ key만 추출해서 Add (OnAdd / OnUpdate / OnDelete)
            ▼
       Workqueue → 중복 제거 메커니즘 적용      # util/workqueue/
            │
            │ ⑤ key 꺼내서 처리 후 Done
            ▼
     Reconcile 함수
      └── Lister로 현재 상태 조회       # listers/core/v1/pod.go
            └─ Indexer.GetByKey()  → 캐시에서 현재 상태 조회 (API 서버 X)
      └── 원하는 상태와 비교 후 API 호출
            └─ 다르면 API 서버에 수정 요청
```

- Reflector
- DeltaFIFO
- Indexer
- SharedIndexInformer

### Reflector

List와 Watch를 통해 변경 이벤트를 감지한다.

아래와 같이 `Until` 메소드를 통해 `ListAndWatchWithContext` 메소드를 반복적으로 실행한다.

```go
// client-go/tools/cache/reflector.go (420-)

// RunWithContext repeatedly uses the reflector's ListAndWatch to fetch all the
// objects and subsequent deltas.
// Run will exit when the context is canceled.
func (r *Reflector) RunWithContext(ctx context.Context) {
	...
	// Until runs the loop immediately (immediate=true) and resets the backoff timer after each
	// successful iteration (sliding=true). See backoff constants at top of file for generalized QPS targets (~0.22 QPS).
	_ = r.delayHandler.Until(ctx, true, true, func(ctx context.Context) (bool, error) {
		if err := r.ListAndWatchWithContext(ctx); err != nil {
			r.watchErrorHandler(ctx, r, err)
		}
		return false, nil
	})
	...
}
```

### DeltaFIFO

Reflector에서 감지된 변경 이벤트를 저장한다. 아래와 같이 `Delta(상태, 오브젝트)` 형태로 DeltaFIFO에 저장된다.

```go
// client-go/tools/cache/reflector.go (178-)

// DeltaType is the type of a change (addition, deletion, etc)
type DeltaType string

// Change type definition
const (
	Added   DeltaType = "Added"
	Updated DeltaType = "Updated"
	Deleted DeltaType = "Deleted"
	Replaced DeltaType = "Replaced"
	ReplacedAll DeltaType = "ReplacedAll"
	Sync DeltaType = "Sync"
	SyncAll DeltaType = "SyncAll"
	Bookmark DeltaType = "Bookmark"
)

// Delta is a member of Deltas (a list of Delta objects) which
// in its turn is the type stored by a DeltaFIFO. It tells you what
// ...
type Delta struct {
	Type   DeltaType
	Object interface{}
}
```

### Indexer

API 서버에서 현재 상태를 조회하지 않고, 캐싱할 수 있는 저장소이다.

Reconcile이 실행될 때, 현재 리소스 상태를 API 서버에 직접 조회하지 않고, Reflector가 미리 동기화해둔 **로컬 캐시(Indexer)**에서 조회한다.

→ 이에 따라 API 서버의 부하를 줄이고 빠르게 조회가 가능하다.

`r.Update()` 로 오브젝트를 수정해도 캐시는 비동기로 갱신되기 때문에, 같은 Reconcile 안에서 바로 `r.Get()` 으로 읽으면 이전 값이 나올 수 있다.

→ 따라서 Operator에서 Spec 업데이트와 Status 업데이트를 분리해야 한다.

```go
obj, exists, err := indexer.GetByKey(key)  // 캐시 조회, API 서버 호출 없음
```

### SharedIndexInformer

Watch 연결과 캐시(Indexer)를 여러 Controller가 공유하는 컴포넌트이다.

`SharedIndexInformer`를 통해 Controller Watch 연결과 로컬 캐시가 공유됨으로써 Controller마다 Watch와 캐시가 존재하여 API 서버 부하가 감소한다.

```
SharedIndexInformer 없이 Controller가 개별 Watch를 사용하는 경우

API Server
├── Watch ──→ Pod Controller A (자체 캐시)
├── Watch ──→ Pod Controller B (자체 캐시)
└── Watch ──→ Pod Controller C (자체 캐시)

Pod 변경 1건 → API Server가 Watch 3개에 각각 전송 → 캐시도 3개가 따로 존재
```

```
SharedIndexInformer를 통해 Watch와 캐시를 공유하는 경우

API Server
   └── Watch ──→ SharedIndexInformer ──→ Indexer (캐시 1개)
                        │
                        ├── EventHandler ──→ Pod Controller A Workqueue
                        ├── EventHandler ──→ Pod Controller B Workqueue
                        └── EventHandler ──→ Pod Controller C Workqueue

Pod 변경 1건 → API Server가 Watch 1개에만 전송 → 캐시도 1개만 존재, 각 Controller는 동일 캐시 공유
```


### Workqueue

Reconcile 작업 단위를 저장하는 큐이다. 중복 제거 메커니즘을 활용하여 불필요한 Reconcile 호출을 줄인다.

Workqueue에는 `namespace/name` 형식의 Key 데이터만 저장한다.

그리고 Workqueue에 추가하는 과정에서 중복 Key는 제거하기 때문에 하나의 리소스에 여러 변경 이벤트가 발생하는 경우에도 **Reconcile 호출 횟수는 최소한으로 진행 된다.**

- `q.queue` : 처리 대기 중인 key 목록
- `q.processing` : 현재 처리 중인 key의 집합
- `q.dirty` : 대기 중 + 처리 중인 key를 포함하는 집합

```go
    // client-go/util/workqueue/queue.go (227-)
    
    func (q *Typed[T]) Add(item T) {
    	q.cond.L.Lock()
    	defer q.cond.L.Unlock()
    	if q.shuttingDown {
    		return
    	}
    	if q.dirty.Has(item) {
    		// the same item is added again before it is processed, call the Touch
    		// function if the queue cares about it (for e.g, reset its priority)
    		if !q.processing.Has(item) {
    			q.queue.Touch(item)
    		}
    		return
    	}
    
    	q.metrics.add(item)
    
    	q.dirty.Insert(item)
    	if q.processing.Has(item) {
    		return
    	}
    
    	q.queue.Push(item)
    	q.cond.Signal()
    }
```

---

## Reconcile

Operator 패턴의 핵심 요소이자, 현재 상태를 선언된 상태로 되돌리기 위한 하나의 **작업 단위**이다.

Reconcile이 실행 되는 과정에서 오류가 발생할 경우, Reconcile은 에러만 반환하고, 재시도는 Workqueue가 처리한다.

```go
err := reconcile(key)       // 성공: Forget(key) → 재시도 이력 초기화
handleErr(err, key)         // 실패: AddRateLimited(key) → 지수 백오프 후 재시도
```

#### Reconcile 설계 원칙

Controller를 구성하는 컴포넌트의 특징을 고려하여 Reconcile을 설계할 필요가 있다.

1. **이벤트 유형을 고려하지 않는다.**

    - 리소스의 현재 상태는 생성, 종료 등 다양한 상태가 있을 수 있는데 이를 코드로 어떻게 정의해야 할까?
    - 그리고 각 케이스에 따른 복구 로직은 또 어떻게 하나하나 전부 작성할 수 있을까?

    이러한 고민이 필요없다. 앞서 설명했듯이, Workqueue에는 리소스의 **key**값만 저장된다.
    
    따라서 **현재 상태와 원하는 상태를 비교**하는 것으로 모든 케이스를 처리한다.

2. **멱등성이 보장되어야 한다.**

    Controller의 일시적인 장애로 특정 오브젝트의 Reconcile 작업이 실패한 상태로 방치될 수 있다.

    Reflector는 이를 방지하기 위해 **Resync**라는 작업을 통해 모든 오브젝트를 주기적으로 다시 DeltaFIFO에 올려 Controller가 동작하게 한다.

    ```go
    // client-go/tools/cache/delta_fifo.go (701-)
   
    // Resync adds, with a Sync type of Delta, every object listed by
    // `f.knownObjects` whose key is not already queued for processing.
    // If `f.knownObjects` is `nil` then Resync does nothing.
    func (f *DeltaFIFO) Resync() error {
    f.lock.Lock()
    defer f.lock.Unlock()
    
        if f.knownObjects == nil {
            return nil
        }
    
        keys := f.knownObjects.ListKeys()
        for _, k := range keys {
            if err := f.syncKeyLocked(k); err != nil {
                return err
            }
        }
        return nil
    }
    ```

    결국 Resync를 통해 아무런 변경 사항이 발생하지 않아도 Reconcile이 다시 발생하는 경우가 있다.
    
    이러한 경우, 실행되어도 **현재 상태와 선언 상태가 같을 때 아무 로직이 실행되지 않고 종료**되어야 한다.

3. **Spec 업데이트와 Status 업데이트를 각각의 Reconcile로 분리한다.**

   Spec의 경우 사용자가 작성(원하는 상태)하고, Status의 경우 Controller가 작성(현재 상태)한다.

   이를 위해 Reconcile 내부에서 각 상태를 수정하기 위해 다음과 같은 메소드를 사용해야 한다.

   ```
   // Spec 수정
   r.Update(ctx, &obj)

   // Status 수정
   r.Status().Update(ctx, &obj)
   이 경우, 아래와 같이 Status가 서브 리소스로 분리되어 내부적으로 엔드포인트가 구분된다.
   ```
   
   ```   
   r.Update()          → /apis/.../myresource/name
   r.Status().Update() → /apis/.../myresource/name/status
   ```
   
   그리고 **API Server**와 **Indexer**의 상태가 일치하지 않을 경우, `resourceVersion`이 일치하지 않아 Reconcile 동작에 충돌이 발생할 수 있다.
   
   현재 상태의 정보를 Indexer라는 로컬 캐시에서 가져오는 것을 고려했을 때, `Update()`를 통해 오브젝트를 수정해도, 캐시에는 비동기로 갱신된다.
   
   → `Update()` 직후에 수정 사항이 캐시에 반영되기 전에 `Get()`을 호출하면 이전 값이 반환될 수 있다.

   ```mermaid
   sequenceDiagram
   participant R as Reconcile
   participant Cache
   participant API as API Server

   R->>Cache: Get()
   Cache-->>R: obj (resourceVersion = "100")

   Note over R: Spec 수정
   Note over R: Status 수정

   R->>API: r.Update() (resourceVersion = "100")
   Note over API: Spec만 업데이트<br/>Status 변경 무시
   API-->>R: 성공 (resourceVersion = "101")

   Note over Cache: 캐시는 아직 "100"<br/>(비동기 갱신 중)

   R->>Cache: 내부적으로 캐시 재조회
   Cache-->>R: obj (resourceVersion = "100") ← 이전 값 반환

   R->>API: r.Status().Update() (resourceVersion = "100")
   API-->>R: ❌ 409 Conflict ("101" 이어야 함)
   ```

   따라서 Spec 수정이 진행되는 동안 Status가 업데이트 될 경우 충돌이 발생할 수 있으므로,
   
   아래와 같이 Spec의 업데이트와 Status의 업데이트를 각각 다른 Reconcile로 분리해서 진행하는 것을 원칙으로 한다.
   
   → 두 Reconcile 사이에 캐시가 업데이트 되는 과정이 생기기 때문에 충돌 발생 X

   ```mermaid
   sequenceDiagram
   participant WQ as Workqueue
   participant R1 as 1번째 Reconcile
   participant Cache
   participant API as API Server
   participant R2 as 2번째 Reconcile

   WQ->>R1: key 전달
   R1->>Cache: Get()
   Cache-->>R1: obj (resourceVersion = "100")

   Note over R1: Spec이 원하는 상태와 다름
   R1->>API: r.Update() → Spec만 수정
   API-->>R1: 성공 (resourceVersion = "101")
   R1-->>WQ: return

   API->>Cache: Watch 이벤트 → 캐시 갱신 (resourceVersion = "101")
   API->>WQ: Spec 변경 감지 → key 재등록

   WQ->>R2: key 전달
   R2->>Cache: Get()
   Cache-->>R2: obj (resourceVersion = "101") ← 갱신된 값

   Note over R2: Spec은 일치<br/>Status가 실제 상태와 다름
   R2->>API: r.Status().Update() → Status만 수정
   API-->>R2: 성공 (resourceVersion = "102")
   R2-->>WQ: return
   ```

→ 처음 Reconcile을 구현할 때 이런 생각으로 막막했지만, `client-go` 의 핵심을 보고 방향성을 찾을 수 있다.

---

## 정리

### Client-go

현재 상태를 선언된 상태로 되돌리는 과정은 다음과 같다.

1. `Reflector`가 변경 이벤트를 감지하고, 이를 Delta로 변환한다.
2. `DeltaFIFO`에 해당 변경 이벤트를 저장하고, 순서가 되면 꺼낸 뒤, `handleDelta()` 를 통해 처리한다.
3. `Indexer` 에 현재 상태를 저장하고, `EventHandler()` 를 호출하여 `Workqueue` 에 전달한다.
4. `Workqueue` 에서 값을 꺼내고, `Indexer`를 조회해서 현재 상태와 다르면 `Reconcile` 함수를 수행한다.

### Reconcile

현재 상태 조회 → 원하는 상태와 비교 → 차이가 있으면 수정, 없으면 종료

즉, 어떻게 변경됐고, 어떻게 수정해야 하는지는 고려할 필요가 없다.

---

**참고 자료**

https://github.com/kubernetes/client-go

