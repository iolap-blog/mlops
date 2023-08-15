package main

import (
	"github.com/gofiber/fiber/v2"
	"k8s.io/client-go/kubernetes"

	"k8s.io/client-go/rest"

	"server/helpers"
)

func main() {

	config, err := rest.InClusterConfig()
	if err != nil {
		panic(err.Error())
	}
	// creates the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	deploymentsClient := clientset.AppsV1().Deployments("namespace")
	serviceClient := clientset.CoreV1().Services("namespace")
	ingressClient := clientset.NetworkingV1().Ingresses("namespace")
	hpaClient := clientset.AutoscalingV2().HorizontalPodAutoscalers("namespace")

	app := fiber.New()

	app.Post("/deploy", func(c *fiber.Ctx) error {

		model := new(helpers.ModelDeploy)
		model.InitModelDefaults()

		if parseErr := c.BodyParser(model); parseErr != nil {
			return fiber.NewError(400, "Wrong json format")
		}

		model_names, endpoint, err := model.ParseModelParams()
		if err != nil {
			return fiber.NewError(400, "Wrong endpoint format")
		}

		crudChannel := make(chan error, 3)

		go helpers.CrudDeployment(deploymentsClient, model, model_names, endpoint, crudChannel)
		go helpers.CrudService(serviceClient, model, endpoint, crudChannel)
		go helpers.CrudIngress(ingressClient, model, endpoint, crudChannel)

		errValue, errCheck := helpers.CheckErrors(crudChannel)
		if errCheck {
			return fiber.NewError(400, errValue)
		}

		errHpa := helpers.CrudHpa(hpaClient, model, endpoint)
		if errHpa != nil {
			return fiber.NewError(400, errHpa.Error())
		}

		response, respErr := helpers.CreateResponse(model, endpoint)
		if respErr != nil {
			return fiber.NewError(400, "Wrong json format")
		}

		return c.Send(response)
	})

	app.Post("/destroy", func(c *fiber.Ctx) error {

		model := new(helpers.ModelDestroy)

		if parseErr := c.BodyParser(model); parseErr != nil {
			return fiber.NewError(400, "Wrong json format")
		}

		err := model.ParseDestroyParams()
		if err != nil {
			fiber.NewError(400, "Wrong name")
		}

		deleteChannel := make(chan error, 4)

		go helpers.DeleteDeployment(deploymentsClient, model.Endpoint, deleteChannel)
		go helpers.DeleteService(serviceClient, model.Endpoint, deleteChannel)
		go helpers.DeleteIngress(ingressClient, model.Endpoint, deleteChannel)
		go helpers.DeleteHpa(hpaClient, model.Endpoint, deleteChannel)

		errValue, errCheck := helpers.CheckErrors(deleteChannel)
		if errCheck {
			return fiber.NewError(400, errValue)
		}

		response, respErr := helpers.CreateDestroyResponse(model, model.Endpoint)
		if respErr != nil {
			return fiber.NewError(400, "Wrong json format")
		}

		return c.Send(response)

	})

	app.Post("/transition", func(c *fiber.Ctx) error {

		model := new(helpers.ModelTransition)

		if parseErr := c.BodyParser(model); parseErr != nil {
			return fiber.NewError(400, "Wrong json format")
		}

		toDestroy, err := model.ParseTransitionParams()
		if err != nil {
			return fiber.NewError(400, "Wrong name")
		}

		transErr := helpers.TransitionService(serviceClient, model, toDestroy)
		if transErr != nil {
			return fiber.NewError(400, transErr.Error())
		}

		transDeleteChannel := make(chan error, 4)

		go helpers.DeleteDeployment(deploymentsClient, model.Endpoint, transDeleteChannel)
		go helpers.DeleteService(serviceClient, toDestroy, transDeleteChannel)
		go helpers.DeleteIngress(ingressClient, toDestroy, transDeleteChannel)
		go helpers.DeleteHpa(hpaClient, model.Endpoint, transDeleteChannel)

		errValue, errCheck := helpers.CheckErrors(transDeleteChannel)
		if errCheck {
			return fiber.NewError(400, errValue)
		}

		response, respErr := helpers.CreateTransResponse(model.Endpoint)
		if respErr != nil {
			return fiber.NewError(400, "Wrong json format")
		}

		return c.Send(response)

	})

	app.Listen(":3000")
}
