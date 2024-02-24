package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/gin-gonic/gin"
	"github.com/gowon-irc/go-gowon"
	"github.com/imroc/req/v3"
	"github.com/jessevdk/go-flags"
)

type Options struct {
	HttpPort  int    `short:"H" long:"http-port" env:"GOWON_HTTP_PORT" default:"8080" description:"http port" validate:"min=1,max=65535"`
	Broker    string `short:"b" long:"broker" env:"GOWON_BROKER" default:"localhost:1883" description:"mqtt broker"`
	GowonHost string `short:"g" log:"gowon-address" env:"GOWON_HOST" required:"true" description:"gowon address"`
}

const (
	moduleName               = "mqttbroker"
	mqttConnectRetryInternal = 5
	mqttDisconnectTimeout    = 1000
)

func defaultPublishHandler(c mqtt.Client, msg mqtt.Message) {
	log.Printf("unexpected message:  %s\n", msg)
}

func onConnectionLostHandler(c mqtt.Client, err error) {
	log.Println("connection to broker lost")
}

func onRecconnectingHandler(c mqtt.Client, opts *mqtt.ClientOptions) {
	log.Println("attempting to reconnect to broker")
}

func onConnectHandler(c mqtt.Client) {
	log.Println("connected to broker")
}

func testHandler(m gowon.Message) (string, error) {
	return "testing", nil
}

func createHttpHandler(mqttClient mqtt.Client) func(c *gin.Context) {
	return func(c *gin.Context) {
		var m gowon.Message

		returnMsg := &gowon.Message{
			Module: moduleName,
			Msg:    "",
			Dest:   m.Dest,
		}

		jsonData, err := io.ReadAll(c.Request.Body)
		if err != nil {
			log.Println(err)
			c.IndentedJSON(http.StatusBadRequest, returnMsg)
		}

		mqttClient.Publish("/gowon/input", 0, false, jsonData)

		c.IndentedJSON(http.StatusOK, returnMsg)
	}
}

func createMessageHandler(gowonHost string) mqtt.MessageHandler {
	return func(mqttClient mqtt.Client, msg mqtt.Message) {
		m, err := gowon.CreateMessageStruct(msg.Payload())
		if err != nil {
			log.Print(err)

			return
		}

		var out gowon.Message

		httpClient := req.C()
		resp, err := httpClient.R().
			SetBody(&m).
			SetSuccessResult(&out).
			Post(gowonHost + "/message")

		if err != nil {
			log.Println(err)
			return
		}

		if !resp.IsSuccessState() {
			log.Printf("Command %s returned an unsuccessful response: %s", m.Command, resp.Status)
			return
		}
	}
}

func createOnConnectHandler(gowonHost string) func(mqtt.Client) {
	log.Println("connected to broker")

	mh := createMessageHandler(gowonHost)

	return func(client mqtt.Client) {
		client.Subscribe("/gowon/output", 0, mh)
		log.Printf("Subscription to topic complete")
	}
}

func main() {
	log.Printf("%s starting\n", moduleName)

	opts := Options{}
	if _, err := flags.Parse(&opts); err != nil {
		log.Fatal(err)
	}

	mqttOpts := mqtt.NewClientOptions()
	mqttOpts.AddBroker(fmt.Sprintf("tcp://%s", opts.Broker))
	mqttOpts.SetClientID(fmt.Sprintf("gowon_%s", moduleName))
	mqttOpts.SetConnectRetry(true)
	mqttOpts.SetConnectRetryInterval(mqttConnectRetryInternal * time.Second)
	mqttOpts.SetAutoReconnect(true)

	mqttOpts.DefaultPublishHandler = defaultPublishHandler
	mqttOpts.OnConnectionLost = onConnectionLostHandler
	mqttOpts.OnReconnecting = onRecconnectingHandler
	mqttOpts.OnConnect = onConnectHandler

	mr := gowon.NewMessageRouter()
	mr.AddCommand("test", testHandler)
	mr.Subscribe(mqttOpts, moduleName)

	log.Print("connecting to broker")

	mqttOpts.OnConnect = createOnConnectHandler(opts.GowonHost)
	c := mqtt.NewClient(mqttOpts)

	httpRouter := gin.Default()
	httpRouter.POST("/message", createHttpHandler(c))

	go func() {
		if err := httpRouter.Run(fmt.Sprintf("0.0.0.0:%d", opts.HttpPort)); err != nil {
			log.Fatal(err)
		}
	}()

	if token := c.Connect(); token.Wait() && token.Error() != nil {
		panic(token.Error())
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	<-sigs

	log.Println("signal caught, exiting")
	c.Disconnect(mqttDisconnectTimeout)
	log.Println("shutdown complete")
}
