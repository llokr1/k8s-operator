# 01. 오퍼레이터 패턴의 기본 개념

## 오퍼레이터 패턴이란

---

오퍼레이터 패턴은 **컴포넌트를 관리하는 운영자의 역할을 소프트웨어 레벨로 구현하는 패턴**이다.

즉, 사용자 정의 리소스(CRD)를 사용하여 CRD는 Custom Resource Definition, 용어 그대로 **정의**를 하는 것이라면, 오퍼레이터는 이렇게 정의된 리소스를 실제로 동작하게 하는 **컨트롤러 패턴**이라고 볼 수 있다.

1. CRD를 정의하면
2. Custom Resource가 생성되고,
3. CRD가 API 서버에 저장되며,
4. Operator가 해당 CRD를 감지하게 된다.
5. 그리고 실제 리소스가 생성되면
6. **Reconciliation Loop**을 통해 사용자가 선언한 상태로 유지한다.

<aside>

**Reconciliation Loop**란, 실제 상태를 원하는 상태로 맞추는 루프를 의미한다.

</aside>

### 오퍼레이터 vs 컨트롤러

오퍼레이터와 컨트롤러는 무슨 차이가 있을까? 

**오퍼레이터 또한 컨트롤러**라고 볼 수 있다. 컨트롤러란, 특정 리소스의 상태를 감시하며 원하는 상태에 맞추기 위한 행동을 의미한다. 

다만, 오퍼레이터는 컨트롤러에 비해 **도메인 지식**이 포함되고, 장애가 발생했을 때, 백업 및 복구가 필요할 때 등, 기본적인 컨트롤러보다 컴포넌트의 운영 방식을 더 세부적으로 선언할 수 있다는 것이 특징이다.

## CRD

---

CRD(Custom Resource Definition)는 기본 쿠버네티스 API의 확장형 엔드포인트로서, **오퍼레이터에 의해 관리되는 컴포넌트를 정의하는 것**이다.

본질적인 목표는 Deployment, ConfigMap과 같이 쿠버네티스에서 제공하는 오브젝트 이외에도 사용자가 별도의 리소스를 정의하여 오브젝트로서 관리하고자 할 때 사용되는 개념이다.

오퍼레이터 패턴을 구현할 때는 **이상적인 리소스의 상태를 표현하는 데 사용**된다.

---

**참고문헌**

https://kubernetes.io/ko/docs/concepts/extend-kubernetes/api-extension/custom-resources/

https://kubernetes.io/ko/docs/concepts/extend-kubernetes/api-extension/apiserver-aggregation/

https://dev.gmarket.com/65