package helpers

import (
	"context"
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	v1 "k8s.io/client-go/kubernetes/typed/apps/v1"
	hpav2 "k8s.io/client-go/kubernetes/typed/autoscaling/v2"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	ingv1 "k8s.io/client-go/kubernetes/typed/networking/v1"
	"k8s.io/client-go/util/retry"

	"regexp"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	apiv1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
)

type Limits struct {
	Memory *int `json:"memory"`
	Cpu    *int `json:"cpu"`
	Gpu    *int `json:"gpu"`
}

type Requests struct {
	Memory *int `json:"memory"`
	Cpu    *int `json:"cpu"`
	Gpu    *int `json:"gpu"`
}

type ModelDeploy struct {
	Model_names    []string `json:"model_names"`
	Endpoint       string   `json:"endpoint"`
	Image          string   `json:"image"`
	Canary         bool     `json:"canary"`
	Canary_weight  *string  `json:"canary_weight"`
	Canary_version *string  `json:"canary_version"`
	Model_stage    string   `json:"model_stage"`
	Limits         Limits   `json:"limits"`
	Requests       Requests `json:"requests"`
}

type ModelDestroy struct {
	Endpoint       string  `json:"endpoint"`
	Canary         bool    `json:"canary"`
	Canary_version *string `json:"canary_version"`
}

type ModelTransition struct {
	Endpoint       string  `json:"endpoint"`
	Canary_version *string `json:"canary_version"`
}

type DeployReturn struct {
	Endpoint       string
	Canary         bool
	Canary_version string
}

type DestroyReturn struct {
	Deleted        string
	Canary         bool
	Canary_version string
}

type TransReturn struct {
	Transition string
}

func (model *ModelDeploy) InitModelDefaults() {
	model.Image = "000000000000.dkr.ecr.us-east-1.amazonaws.com/name:latest"
	model.Model_stage = "Production"
}

func (model *ModelDeploy) ParseModelParams() (string, string, error) {

	if model.Limits.Gpu != nil && model.Requests.Gpu != nil {
		if *model.Limits.Gpu != *model.Requests.Gpu {
			*model.Requests.Gpu = *model.Limits.Gpu
		}
	}

	model_names := strings.Join(model.Model_names, ",")

	r, err := regexp.Compile(`[\W_]`)
	if err != nil {
		return "", "", err
	}
	parsed_endpoint := r.ReplaceAllString(strings.ToLower(model.Endpoint), "")

	runes := []rune(parsed_endpoint)

	var endpoint string
	if len(runes) >= 14 {
		endpoint = string(runes[:13])
	} else {
		endpoint = string(runes)
	}

	return model_names, endpoint, err

}

func (model *ModelDestroy) ParseDestroyParams() error {

	r, err := regexp.Compile(`[\W_]`)
	if err != nil {
		return err
	}
	parsed_endpoint := r.ReplaceAllString(strings.ToLower(model.Endpoint), "")

	runes := []rune(parsed_endpoint)

	if len(runes) >= 14 {
		model.Endpoint = string(runes[:13])
	} else {
		model.Endpoint = string(runes)
	}

	if model.Canary {
		model.Endpoint = model.Endpoint + *model.Canary_version
	}

	return nil
}

func (model *ModelTransition) ParseTransitionParams() (string, error) {

	r, err := regexp.Compile(`[\W_]`)
	if err != nil {
		return "", err
	}
	parsed_endpoint := r.ReplaceAllString(strings.ToLower(model.Endpoint), "")

	runes := []rune(parsed_endpoint)

	if len(runes) >= 14 {
		model.Endpoint = string(runes[:13])
	} else {
		model.Endpoint = string(runes)
	}

	toDestroy := ""
	if model.Canary_version != nil {
		toDestroy = model.Endpoint + *model.Canary_version
	}

	return toDestroy, nil
}

func (model *ModelDeploy) initResources() (resources apiv1.ResourceRequirements) {

	resources = apiv1.ResourceRequirements{}

	if model.Limits.Memory != nil {
		resources.Limits = apiv1.ResourceList{
			apiv1.ResourceName("memory"): *resource.NewQuantity(int64(*model.Limits.Memory), resource.BinarySI),
		}
	}

	if model.Limits.Cpu != nil {
		resources.Limits = apiv1.ResourceList{
			apiv1.ResourceName("cpu"): *resource.NewMilliQuantity(int64(*model.Limits.Cpu), resource.DecimalSI),
		}
	}

	if model.Limits.Gpu != nil {
		resources.Limits = apiv1.ResourceList{
			apiv1.ResourceName("nvidia.com/gpu"): *resource.NewMilliQuantity(int64(*model.Limits.Gpu), resource.DecimalSI),
		}
	}

	if model.Requests.Memory != nil {
		resources.Requests = apiv1.ResourceList{
			apiv1.ResourceName("memory"): *resource.NewQuantity(int64(*model.Requests.Memory), resource.BinarySI),
		}
	}

	if model.Requests.Cpu != nil {
		resources.Requests = apiv1.ResourceList{
			apiv1.ResourceName("cpu"): *resource.NewMilliQuantity(int64(*model.Requests.Cpu), resource.DecimalSI),
		}
	}

	if model.Requests.Gpu != nil {
		resources.Requests = apiv1.ResourceList{
			apiv1.ResourceName("nvidia.com/gpu"): *resource.NewMilliQuantity(int64(*model.Requests.Gpu), resource.DecimalSI),
		}
	}

	return
}

func newDeployment(model *ModelDeploy, model_names, endpoint string) *appsv1.Deployment {

	var canary_version string
	if model.Canary_version != nil {
		canary_version = *model.Canary_version
	} else {
		canary_version = ""
	}

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      endpoint + canary_version,
			Namespace: "namespace",
			Labels: map[string]string{
				"app": endpoint + canary_version,
			},
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": endpoint + canary_version,
				},
			},
			Template: apiv1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": endpoint + canary_version,
					},
				},
				Spec: apiv1.PodSpec{
					Containers: []apiv1.Container{
						{
							Name:            endpoint + canary_version,
							Image:           model.Image,
							ImagePullPolicy: apiv1.PullPolicy("Always"),
							Ports: []apiv1.ContainerPort{
								{
									Name:          endpoint + canary_version,
									ContainerPort: 8080,
								},
							},
							Env: []apiv1.EnvVar{
								{Name: "MODEL_NAMES", Value: model_names},
								{Name: "ENDPOINT", Value: endpoint},
								{Name: "MODEL_STAGE", Value: model.Model_stage},
							},
							Resources: model.initResources(),
						},
					},
				},
			},
		},
	}

	return deployment
}

func newService(model *ModelDeploy, endpoint string) *apiv1.Service {

	var canary_version string
	if model.Canary_version != nil {
		canary_version = *model.Canary_version
	} else {
		canary_version = ""
	}

	service := &apiv1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      endpoint + canary_version,
			Namespace: "namespace",
		},
		Spec: apiv1.ServiceSpec{
			Selector: map[string]string{
				"app": endpoint + canary_version,
			},
			Ports: []apiv1.ServicePort{
				{
					Name:       endpoint + canary_version,
					Protocol:   apiv1.ProtocolTCP,
					Port:       8080,
					TargetPort: intstr.FromString(endpoint + canary_version),
				},
			},
		},
	}

	return service
}

func createIngressAnnotations(model *ModelDeploy) (annotations map[string]string) {

	if !model.Canary {
		annotations = map[string]string{
			"nginx.ingress.kubernetes.io/rewrite-target": "/$2",
		}
	} else {
		annotations = map[string]string{
			"nginx.ingress.kubernetes.io/rewrite-target": "/$2",
			"nginx.ingress.kubernetes.io/canary":         "true",
			"nginx.ingress.kubernetes.io/canary-weight":  *model.Canary_weight,
		}
	}

	return annotations
}

func newIngress(model *ModelDeploy, endpoint string) *networkingv1.Ingress {

	var canary_version string
	if model.Canary_version != nil {
		canary_version = *model.Canary_version
	} else {
		canary_version = ""
	}

	annotations := createIngressAnnotations(model)

	ingressClass := new(string)
	*ingressClass = "inference"

	ingressPath := new(networkingv1.PathType)
	*ingressPath = networkingv1.PathTypePrefix

	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:        endpoint + canary_version,
			Namespace:   "namespace",
			Annotations: annotations,
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: ingressClass,
			Rules: []networkingv1.IngressRule{
				{
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     fmt.Sprintf("/()(invocations/%s.*)", endpoint),
									PathType: ingressPath,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: endpoint + canary_version,
											Port: networkingv1.ServiceBackendPort{
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

	return ingress
}

func newHpa(model *ModelDeploy, endpoint string) *autoscalingv2.HorizontalPodAutoscaler {

	var canary_version string
	if model.Canary_version != nil {
		canary_version = *model.Canary_version
	} else {
		canary_version = ""
	}

	minScale := new(int32)
	*minScale = 1

	cpuPercent := new(int32)
	*cpuPercent = 50

	hpa := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      endpoint,
			Namespace: "namespace",
		},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				Kind:       "Deployment",
				APIVersion: "apps/v1",
				Name:       endpoint + canary_version,
			},
			MinReplicas: minScale,
			MaxReplicas: 10,
			Metrics: []autoscalingv2.MetricSpec{
				{
					Type: autoscalingv2.MetricSourceType("Resource"),
					Resource: &autoscalingv2.ResourceMetricSource{
						Name: apiv1.ResourceName("cpu"),
						Target: autoscalingv2.MetricTarget{
							Type:               autoscalingv2.MetricTargetType("Utilization"),
							AverageUtilization: cpuPercent,
						},
					},
				},
			},
		},
	}

	return hpa
}

func CrudDeployment(
	deploymentsClient v1.DeploymentInterface,
	model *ModelDeploy,
	model_names, endpoint string,
	crudChannel chan error,
) {

	deployment := newDeployment(model, model_names, endpoint)

	_, getErr := deploymentsClient.Get(context.TODO(), endpoint, metav1.GetOptions{})

	if getErr != nil {
		if strings.HasSuffix(getErr.Error(), "not found") {
			fmt.Println("Creating deployment...")
			result, err := deploymentsClient.Create(
				context.TODO(), deployment, metav1.CreateOptions{},
			)
			if err != nil {
				crudChannel <- err
				return
			}
			fmt.Printf("Created deployment %q.\n", result.GetObjectMeta().GetName())
		} else {
			crudChannel <- getErr
			return
		}
	} else {
		fmt.Println("Updating deployment...")
		updateResult, updateErr := deploymentsClient.Update(
			context.TODO(), deployment, metav1.UpdateOptions{},
		)
		if updateErr != nil {
			crudChannel <- updateErr
			return
		}
		fmt.Printf("Updated deployment %q.\n", updateResult.GetObjectMeta().GetName())
	}

	crudChannel <- nil

}

func CrudService(
	serviceClient corev1.ServiceInterface,
	model *ModelDeploy,
	endpoint string,
	crudChannel chan error,
) {

	service := newService(model, endpoint)

	_, getErr := serviceClient.Get(context.TODO(), endpoint, metav1.GetOptions{})

	if getErr != nil {
		if strings.HasSuffix(getErr.Error(), "not found") {
			fmt.Println("Creating service...")
			result, err := serviceClient.Create(
				context.TODO(), service, metav1.CreateOptions{},
			)
			if err != nil {
				crudChannel <- err
				return
			}
			fmt.Printf("Created service %q.\n", result.GetObjectMeta().GetName())
		} else {
			crudChannel <- getErr
			return
		}
	} else {
		fmt.Println("Updating service...")
		updateResult, updateErr := serviceClient.Update(
			context.TODO(), service, metav1.UpdateOptions{},
		)
		if updateErr != nil {
			crudChannel <- updateErr
			return
		}
		fmt.Printf("Updated service %q.\n", updateResult.GetObjectMeta().GetName())
	}

	crudChannel <- nil

}

func TransitionService(
	serviceClient corev1.ServiceInterface, model *ModelTransition, toDestroy string,
) error {

	fmt.Println("Updating deployment...")

	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Retrieve the latest version of Deployment before attempting update
		// RetryOnConflict uses exponential backoff to avoid exhausting the apiserver
		service, getErr := serviceClient.Get(context.TODO(), model.Endpoint, metav1.GetOptions{})
		if getErr != nil {
			return getErr
		}
		service.Spec.Selector = map[string]string{
			"app": model.Endpoint + *model.Canary_version,
		}
		service.Spec.Ports[0].Name = model.Endpoint
		service.Spec.Ports[0].TargetPort = intstr.FromString(model.Endpoint + *model.Canary_version)
		_, updateErr := serviceClient.Update(context.TODO(), service, metav1.UpdateOptions{})
		return updateErr
	})

	if retryErr != nil {
		return retryErr
	}

	fmt.Println("Updated deployment...")

	return nil
}

func CrudIngress(
	ingressClient ingv1.IngressInterface,
	model *ModelDeploy,
	endpoint string,
	crudChannel chan error,
) {

	ingress := newIngress(model, endpoint)

	_, getErr := ingressClient.Get(context.TODO(), endpoint, metav1.GetOptions{})

	if getErr != nil {
		if strings.HasSuffix(getErr.Error(), "not found") {
			fmt.Println("Creating ingress...")
			result, err := ingressClient.Create(
				context.TODO(), ingress, metav1.CreateOptions{},
			)
			if err != nil {
				crudChannel <- err
				return
			}
			fmt.Printf("Created ingress %q.\n", result.GetObjectMeta().GetName())
		} else {
			crudChannel <- getErr
			return
		}
	} else {
		fmt.Println("Updating ingress...")
		updateResult, updateErr := ingressClient.Update(
			context.TODO(), ingress, metav1.UpdateOptions{},
		)
		if updateErr != nil {
			crudChannel <- updateErr
			return
		}
		fmt.Printf("Updated ingress %q.\n", updateResult.GetObjectMeta().GetName())
	}

	crudChannel <- nil

}

func CrudHpa(
	hpaClient hpav2.HorizontalPodAutoscalerInterface,
	model *ModelDeploy,
	endpoint string,
) error {

	hpa := newHpa(model, endpoint)

	_, getErr := hpaClient.Get(context.TODO(), endpoint, metav1.GetOptions{})

	if getErr != nil {
		if strings.HasSuffix(getErr.Error(), "not found") {
			fmt.Println("Creating hpa...")
			result, err := hpaClient.Create(
				context.TODO(), hpa, metav1.CreateOptions{},
			)
			if err != nil {
				return err
			}
			fmt.Printf("Created hpa %q.\n", result.GetObjectMeta().GetName())
		} else {
			return getErr
		}
	} else {
		fmt.Println("Updating hpa...")
		updateResult, updateErr := hpaClient.Update(
			context.TODO(), hpa, metav1.UpdateOptions{},
		)
		if updateErr != nil {
			return updateErr
		}
		fmt.Printf("Updated hpa %q.\n", updateResult.GetObjectMeta().GetName())
	}

	return nil
}

func CreateResponse(model *ModelDeploy, endpoint string) ([]byte, error) {

	message := new(DeployReturn)
	message.Endpoint = endpoint
	message.Canary = model.Canary

	if model.Canary_version != nil {
		message.Canary_version = *model.Canary_version
	}

	message_parsed, error := json.Marshal(message)

	return message_parsed, error
}

func CreateDestroyResponse(model *ModelDestroy, endpoint string) ([]byte, error) {

	message := new(DestroyReturn)
	message.Deleted = "/invocations/" + endpoint
	message.Canary = model.Canary

	if model.Canary_version != nil {
		message.Canary_version = *model.Canary_version
	}

	message_parsed, error := json.Marshal(message)

	return message_parsed, error
}

func CreateTransResponse(endpoint string) ([]byte, error) {

	message := new(TransReturn)
	message.Transition = "/invocations/" + endpoint

	message_parsed, error := json.Marshal(message)

	return message_parsed, error
}

func CheckErrors(channel chan error) (string, bool) {

	errorsSet := make([]error, 0)
	i := 0
	for {
		crudErr := <-channel
		i++
		if crudErr != nil {
			errorsSet = append(errorsSet, crudErr)
		}
		if i == cap(channel) {
			break
		}

	}

	valueErr := ""
	checkErr := false

	for _, value := range errorsSet {
		if value != nil {
			valueErr += value.Error() + " \n"
			checkErr = true
		}
	}

	return valueErr, checkErr
}

func DeleteDeployment(
	deploymentsClient v1.DeploymentInterface, endpoint string, deleteChannel chan error,
) {
	fmt.Println("Deleting deployment...")
	deletePolicy := metav1.DeletePropagationForeground
	if err := deploymentsClient.Delete(context.TODO(), endpoint, metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}); err != nil {
		deleteChannel <- err
		return
	}
	fmt.Println("Deleted deployment.")
	deleteChannel <- nil
}

func DeleteService(
	serviceClient corev1.ServiceInterface, endpoint string, deleteChannel chan error,
) {
	fmt.Println("Deleting service...")
	deletePolicy := metav1.DeletePropagationForeground
	if err := serviceClient.Delete(context.TODO(), endpoint, metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}); err != nil {
		deleteChannel <- err
		return
	}
	fmt.Println("Deleted service.")
	deleteChannel <- nil
}

func DeleteIngress(
	ingressClient ingv1.IngressInterface, endpoint string, deleteChannel chan error,
) {
	fmt.Println("Deleting ingress...")
	deletePolicy := metav1.DeletePropagationForeground
	if err := ingressClient.Delete(context.TODO(), endpoint, metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}); err != nil {
		deleteChannel <- err
		return
	}
	fmt.Println("Deleted ingress.")
	deleteChannel <- nil
}

func DeleteHpa(
	hpaClient hpav2.HorizontalPodAutoscalerInterface, endpoint string, deleteChannel chan error,
) {
	fmt.Println("Deleting hpa...")
	deletePolicy := metav1.DeletePropagationForeground
	if err := hpaClient.Delete(context.TODO(), endpoint, metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}); err != nil {
		deleteChannel <- err
		return
	}
	fmt.Println("Deleted hpa.")
	deleteChannel <- nil
}
