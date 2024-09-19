package main

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"time"

	"cliente/devservice"
	"cliente/iocservice"

	MQTT "github.com/eclipse/paho.mqtt.golang"
	"github.com/hooklift/gowsdl/soap"
)

var client MQTT.Client
var defClient MQTT.Client
var currentTopic string
var currentDefTopic string
var currentPayload string
var currentDefPayload string
var printCh = make(chan struct{})
var printCh2 = make(chan struct{})
var exitCh = make(chan struct{})
var exitCh2 = make(chan struct{})
var wg sync.WaitGroup
var num int
var token int32
var motorsInit bool = false
var afastando int
var aprox int

// const serviceURL = "http://172.26.1.1" // Endereço do SOAP
var devclient = soap.NewClient("http://172.26.1.1")
var iocclient = soap.NewClient("http://172.26.1.1")

type payloadDefense struct {
	Method string `json:"method"`
	Info   []struct {
		DeviceCode        string   `json:"deviceCode"`
		ChannelSeq        int      `json:"channelSeq"`
		UnitType          int      `json:"unitType"`
		UnitSeq           int      `json:"unitSeq"`
		NodeType          string   `json:"nodeType"`
		NodeCode          string   `json:"nodeCode"`
		AlarmCode         string   `json:"alarmCode"`
		AlarmStat         string   `json:"alarmStat"`
		AlarmType         string   `json:"alarmType"`
		AlarmGrade        string   `json:"alarmGrade"`
		AlarmPicture      string   `json:"alarmPicture"`
		AlarmDate         string   `json:"alarmDate"`
		Memo              string   `json:"memo"`
		ExtData           string   `json:"extData"`
		LinkVideoChannels []any    `json:"linkVideoChannels"`
		UserIds           []any    `json:"userIds"`
		AlarmSourceName   string   `json:"alarmSourceName"`
		RuleThreshold     int      `json:"ruleThreshold"`
		StayNumber        int      `json:"stayNumber"`
		PlanTemplateID    string   `json:"planTemplateId"`
		DeviceName        string   `json:"deviceName"`
		LinkedOutput      string   `json:"linkedOutput"`
		MapIds            []string `json:"mapIds"`
	} `json:"info"`
}

func onMessageReceived(client MQTT.Client, message MQTT.Message) {
	currentPayload = string(message.Payload())
	select {
	case printCh2 <- struct{}{}:
	default:
	}
}

func messageHandler(defClient MQTT.Client, event MQTT.Message) {
	currentDefPayload = string(event.Payload())
	fmt.Printf("Current payload: %s\n", currentDefPayload)
	fmt.Printf("Topic: %s\n", event.Topic())

	var evento payloadDefense

	// Use json.Unmarshal to decode JSON into the struct
	err := json.Unmarshal(event.Payload(), &evento)
	if err != nil {
		fmt.Printf("Error decoding JSON: %v\n", err)
		return
	}

	// Evento facial -> num = 1 (ESP 1), vai ate a camera
	if evento.Info[0].AlarmType == "100001" && evento.Info[0].DeviceName == "Facial" {
		num = 1
		fmt.Println("num 1")
		// Signal that a new message has arrived and can be printed
		select {
		case printCh <- struct{}{}:
		default:
		}
	}

	// Evento softtrigger -> num = 2 (ESP 2), volta pra posicao inicial
	if evento.Info[0].AlarmType == "11000001" && evento.Info[0].DeviceName == "Facial" {
		num = 2
		fmt.Println("num 2")
		// Signal that a new message has arrived and can be printed
		select {
		case printCh <- struct{}{}:
		default:
		}
	}
	// Evento desconexao -> num = 3 (ESP 3), vai ate o dispositivo ficticio
	if evento.Info[0].AlarmType == "16" && evento.Info[0].DeviceName == "Teste" {
		num = 3
		fmt.Println("num 3")
		// Signal that a new message has arrived and can be printed
		select {
		case printCh <- struct{}{}:
		default:
		}
	}
}

func generateSoapToken() {
	accessMode := devservice.EAccessSERVICEREADWRITE

	// Create an instance of the Open request
	openRequest := &devservice.Open{
		Id:         2, // cam - 1	robotino - 2
		AccessMode: &accessMode,
	}

	// Make the SOAP call to open the connection
	openResponse, err := devservice.NewDeviceManagerPortType(devclient).Open(openRequest)
	if err != nil {
		log.Fatalf("Error opening connection to SOAP: %v", err)
	}

	// Process the response
	token = openResponse.Token
	fmt.Printf("SOAP Token: %d\n", openResponse.Token)
}

func keepAlive() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			//fmt.Println("Keep-alive: Sending a keep-alive signal...")
			keepAliveRequest := &devservice.KeepAlive{
				Token: token,
			}
			_, err := devservice.NewDeviceManagerPortType(devclient).KeepAlive(keepAliveRequest)
			if err != nil {
				fmt.Printf("Error keeping token alive: %v\n", err)
				break
			}
		case <-exitCh:
			return
		}
	}
}

// girar o robo 180 graus
func girar180() {
	i := 0
	for i < 40 {
		// Construct the setVelocity struct
		setVelocity := &iocservice.SetVelocity{
			Token:  token, // Use the token received from devservice
			VX:     0,
			VY:     0,
			VOmega: 45,
		}
		// Send the setVelocity struct to set the velocity
		_, err := iocservice.NewIOControlPortType(iocclient).SetVelocity(setVelocity)
		if err != nil {
			log.Fatalf("Error setting velocity to spin 180: %v", err)
		}
		duration := 100 * time.Millisecond
		time.Sleep(duration)
		i++
	}
}

// ref = reference 	cur = current
func verifyApproach(ref int, cur int) {
	reff := float64(ref)
	curr := float64(cur)
	//afastando
	if reff/curr < 1 {
		afastando++
		return
	}
	//arpoximando
	if reff/curr > 1 {
		aprox++
		return
	}
}

func move(obj int) {
	//indices de afastamento e aproximacao
	afastando = 0
	aprox = 0
	//armazena o valor de RSSI recebido no inicio do movimento e o converte para inteiro
	refPayload, err := strconv.Atoi(currentPayload)
	fmt.Printf("%d", refPayload)
	if err != nil {
		fmt.Printf("could not convert reference payload to integer.\n")
		return
	}
	//initialize motors
	if !motorsInit {
		for i := 0; i < 3; i++ {
			setMotorConstants := &iocservice.SetMotorConstants{
				Token: token, // Use the token received from devservice
				Motor: byte(i),
				Kp:    25,
				Kd:    25,
				Ki:    25,
			}
			_, err := iocservice.NewIOControlPortType(iocclient).SetMotorConstants(setMotorConstants)
			if err != nil {
				log.Fatalf("Error setting motor %d constants: %v", i, err)
			}
		}
		fmt.Printf("motors initialized.\n")
		motorsInit = true
	}
	//zera contador de aproximacao
	contApproach := 0
	//go foward
	for {
		// Construct the setVelocity struct
		setVelocity := &iocservice.SetVelocity{
			Token:  token, // Use the token received from devservice
			VX:     100,
			VY:     0,
			VOmega: 0,
		}
		// Send the setVelocity struct to set the velocity
		_, err := iocservice.NewIOControlPortType(iocclient).SetVelocity(setVelocity)
		if err != nil {
			log.Fatalf("Error setting velocity: %v", err)
		}

		//converte todo RSSI recebido para inteiro
		payloadAsInt, err := strconv.Atoi(currentPayload)
		fmt.Printf("%d", payloadAsInt)
		if err != nil {
			fmt.Printf("could not convert payload to integer.\n")
			return
		}

		//Verifica se esta se aproximando ou afastando do alvo
		verifyApproach(refPayload, payloadAsInt)
		contApproach++
		if contApproach > 40 {
			if aprox > afastando {
				fmt.Printf("Robo se aproximando")
				afastando = 0
				aprox = 0
			}
			if afastando > aprox {
				fmt.Printf("Robo se afastando, efetuar manobra")
				refPayload = payloadAsInt
				afastando = 0
				aprox = 0
				girar180()
			}
			contApproach = 0
		}

		//encerra o movimento caso proximo ao alvo (RSSI < -40)
		if payloadAsInt > -45 {
			//envia um evento ao defense indicando chegada
			sendEventToDefense("11", "22")
			fmt.Println("Robotino chegou ao objetivo.")
			//finaliza loop de movimento
			return
		}
		duration := 150 * time.Millisecond
		time.Sleep(duration)
	}
}

func selectNumber() {
	reader := bufio.NewReader(os.Stdin)
	fmt.Println("numero:")
	_, err := fmt.Fscanf(reader, "%d\n", &num)
	if err != nil || num < 1 || num > 3 {
		fmt.Printf("Invalid input.")
		return
	}
}

func subscribeToTopic(topic string) {
	if currentTopic != "" {
		// Unsubscribe from the previous topic
		if token := client.Unsubscribe(currentTopic); token.Wait() && token.Error() != nil {
			log.Printf("Error unsubscribing from topic %s: %v\n", currentTopic, token.Error())
		}
	}

	fmt.Printf("Subscribing to topic: %s\n", topic)
	if token := client.Subscribe(topic, 0, onMessageReceived); token.Wait() && token.Error() != nil {
		log.Fatalf("Error subscribing to topic: %v\n", token.Error())
	}
	fmt.Printf("Subscribed to topic: %s\n", topic)
	currentTopic = topic
}

func subscribeDefense(defTopic string) {
	fmt.Printf("Subscribing to topic: %s\n", defTopic)
	if token := defClient.Subscribe(defTopic, 0, messageHandler); token.Wait() && token.Error() != nil {
		log.Fatalf("Error subscribing to topic: %v\n", token.Error())
	}
	fmt.Printf("Subscribed to topic: %s\n", defTopic)
	currentDefTopic = defTopic
}

func main() {
	generateSoapToken()
	go keepAlive()

	clientID := "mqtt-client-id"
	//Broker RSSI
	broker := "tcp://172.26.1.100:1883"
	//Topicos RSSI
	topics := [3]string{"esp_1/rssi", "esp_2/rssi", "esp_3/rssi"}
	//Conexao ao broker RSSI
	opts := MQTT.NewClientOptions().AddBroker(broker).SetClientID(clientID)
	client = MQTT.NewClient(opts)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		log.Fatalf("Error connecting to RSSI MQTT broker: %v\n", token.Error())
	}
	//Broker Defense
	defenseBroker := "mqtts://10.0.0.10:1884"
	//Credenciais broker Defense
	MQpass := "7POXEN13n4z92DIk"
	MQuser := "admin"
	//Topico Eventos
	defTopic := "mq/alarm/msg/topic/1"
	config := &tls.Config{
		InsecureSkipVerify: true,
	}
	defOpts := MQTT.NewClientOptions().AddBroker(defenseBroker).SetClientID(clientID)
	defOpts.SetTLSConfig(config)
	defOpts.Password = MQpass
	defOpts.Username = MQuser
	defClient = MQTT.NewClient(defOpts)
	//Conexao ao broker Defense
	if token := defClient.Connect(); token.Wait() && token.Error() != nil {
		log.Fatalf("Error connecting to Defense MQTT broker: %v\n", token.Error())
	}

	//end

	//subscribe to # topic at Defense broker
	subscribeDefense(defTopic)

	fmt.Println("Esperando evento do Defense")

	wg.Add(1)
	go func() {
		defer wg.Done()
		fmt.Print("Em espera...")
		for {
			select {
			case <-printCh:
				selectedTopic := topics[num-1]
				subscribeToTopic(selectedTopic)
				select {
				case <-printCh2:
					//manda evento ao Defense indicando que esta iniciando rotina (pop-up de camera)
					sendEventToDefense("1", "2")
					// Print the payload
					fmt.Printf("Current payload: %s\n", currentPayload)
					payloadAsInt, err := strconv.Atoi(currentPayload)
					fmt.Printf("%d", payloadAsInt)
					if err != nil {
						fmt.Printf("could not convert payload to integer.\n")
						return
					}
					if payloadAsInt < -40 {
						//if currentPayload != "-40" || currentPayload != "-39" || currentPayload != "-38" {
						fmt.Printf("Moving until get close.\n")
						move(num)
					}
					num = 0
					fmt.Printf("Rotina, finalizada.\n")
					/*fmt.Println("Esperando evento do Defense")
					for num != 1 && num != 2 && num != 3 {
					}
					fmt.Println("Evento recebido. Acionando Robotino.")
					selectedTopic = topics[num-1]
					subscribeToTopic(selectedTopic)*/
					break
				}

			case <-exitCh:
				// Exit the goroutine when requested
				return
			}
		}
	}()

	/*
		reader := bufio.NewReader(os.Stdin)
		wg.Add(1)
		go func() {
			defer wg.Done()
			fmt.Print("Em espera...")
			for {
				select {
				case <-printCh:
					fmt.Println("Press 'p' to print the payload, 'q' to quit the current topic, or 'x' to exit the program.")
					fmt.Print("Your choice: ")
					input, _ := reader.ReadString('\n')
					input = strings.TrimSpace(input)
					switch input {
					case "p":
						//manda evento ao Defense indicando que esta iniciando rotina (pop-up de camera)
						sendEventToDefense("1", "2")
						// Print the payload
						fmt.Printf("Current payload: %s\n", currentPayload)
						payloadAsInt, err := strconv.Atoi(currentPayload)
						fmt.Printf("%d", payloadAsInt)
						if err != nil {
							fmt.Printf("could not convert payload to integer.\n")
							return
						}
						if payloadAsInt < -40 {
							//if currentPayload != "-40" || currentPayload != "-39" || currentPayload != "-38" {
							fmt.Printf("Moving until get close.\n")
							move(num)
						}
						fmt.Printf("Seesao finalizada. Esperando novo evento.\n")

					case "q":
						selectNumber()
						selectedTopic = topics[num-1]
						// Unsubscribe from the current topic and return to the initial state
						subscribeToTopic(selectedTopic)
					case "x":
						// Exit the program
						close(exitCh)
						fmt.Println("Programa encerrado.")
						client.Disconnect(250)
						fmt.Println("Disconnected from MQTT broker")
						return
					default:
						fmt.Println("Invalid choice.")
					}
				case <-exitCh:
					// Exit the goroutine when requested
					return
				}
			}
		}()
	*/
	// Wait for Ctrl+C to exit
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	<-c

	// Signal the exit to the message processing goroutine
	close(exitCh)
	wg.Wait()

	// Disconnect from the MQTT broker
	client.Disconnect(250)
	fmt.Println("Disconnected from MQTT broker")
}

// DEFENSE IA comm

type defenseTokenResponseStruct struct {
	Code int    `json:"code"`
	Desc string `json:"desc"`
	Data struct {
		Token    string `json:"token"`
		Duration string `json:"duration"`
	} `json:"data"`
}

var defenseIP string
var defensePort int
var defenseAccessKey string
var defenseSecretKey string

func sendEventToDefense(eventType string, eventSource string) {
	//Informacoes de conexao ao Defense IA
	defenseIP = "10.0.0.10"
	defensePort = 8000
	defenseAccessKey = "5mHo9a2713i9U9x2Bo5Qz4V9"
	defenseSecretKey = "EdCORWY6N2hqv29W1630gYu="
	nowTimestamp := time.Now()
	nowTimestamp.UnixMilli()
	data := []byte(`{
		"accessKey": "` + defenseAccessKey + `",
		"signature": "` + HMAC256(fmt.Sprintf("%d", nowTimestamp.UnixMilli()), defenseSecretKey) + `",
		"timestamp": "` + fmt.Sprintf("%d", nowTimestamp.UnixMilli()) + `"
	}`)
	//Conexao ao Defense IA
	url := fmt.Sprintf("http://%s:%d/ecos/api/v1.1/%s", defenseIP, defensePort, "account/authorize")
	fmt.Printf("url: %s\n", url)
	body, _ := defenseRequest(http.MethodPost, url, bytes.NewBuffer(data), "")
	var defenseTokenResponse defenseTokenResponseStruct
	json.Unmarshal(body, &defenseTokenResponse)
	fmt.Printf("Body: %s\n", body)
	//Envio de evento
	pushEventToDefense(defenseTokenResponse.Data.Token, eventType, eventSource, "1233", "evento do robotino")
}

func HMAC256(payload string, secret string) string {
	sig := hmac.New(sha256.New, []byte(secret))
	sig.Write([]byte(payload))
	sha1_hash := hex.EncodeToString(sig.Sum(nil))
	return sha1_hash
}

func pushEventToDefense(token string, eventTypeCode string, eventSourceCode string, eventId string, remark string) { //device 2 abertura
	nowTimestamp := time.Now()
	nowTimestamp.Unix()
	//Estrutura pacote
	data := []byte(`{
		"eventId": "` + eventId + `",
		"eventTime": ` + fmt.Sprintf("%d", nowTimestamp.Unix()) + `,
		"eventSourceCode": "` + eventSourceCode + `",
		"eventTypeCode": "` + eventTypeCode + `",
		"remark": "` + remark + `"
	}`)
	//Comando para enviar evento
	url := fmt.Sprintf("http://%s:%d/ecos/api/v1.1/%s", defenseIP, defensePort, "bridge/event/push")
	body, err := defenseRequest(http.MethodPost, url, bytes.NewBuffer(data), token)
	fmt.Printf("Body: %s\n", body)
	if err != nil {
		fmt.Printf("err: %s\n", err)
	}
}

func CreateDeviceOnBridge(eventSourceCode string, eventSourceName string, token string, defenseIp string, defensePort int) {

	url := fmt.Sprintf("http://%s:%d/ecos/api/v1.1/bridge/1/event/type", defenseIp, defensePort)
	data := []byte(`{
            "eventTypeCode": " ` + eventSourceCode + ` ",
            "eventTypeName": " ` + eventSourceName + ` "
        }`)
	body, _ := defenseRequest(http.MethodPost, url, bytes.NewBuffer(data), token)
	//fmt.Println(err, "err")
	//fmt.Println("[SUCCESS] Device was created")
	fmt.Println("This event creates a new device on bridge", string(body))
	// end creation

}

func contains(arr []bool) bool {
	for _, u := range arr {
		if u == false {
			return false
		}
	}
	return true
}

func defenseRequest(requestType string, requestURL string, requestData *bytes.Buffer, token string) ([]byte, error) {

	var body []byte
	var err error
	client := &http.Client{}
	req, err := http.NewRequest(requestType, requestURL, requestData)
	if err != nil {
		fmt.Println(err, "Erro1")
	}
	req.Header.Add("Content-Type", "application/json")
	if token != "" {
		req.Header.Add("X-Subject-Token", token)
	}

	resp, err := client.Do(req)
	if err != nil {
		fmt.Println(err, "Erro2")
	} else {

		body, err = io.ReadAll(resp.Body)
	}
	return body, err
}
