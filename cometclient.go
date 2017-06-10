package main

import (
	"bytes"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	maxMessageSize = 4096
	//pingPeriod     = (pongWait * 9) / 10
	pingPeriod     = 300 * time.Second
	readTimeout = 1200 * time.Second
)

var wsupgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

var (
	newline = []byte{'\n'}
	space   = []byte{' '}
)

type CometClient struct {
	hub     *CometHub
	conn    *websocket.Conn
	send    chan []byte
	receive chan []byte

	bizId     string
	bizType   string
	channelId string
	page      string

	closeChan             chan struct{}
	adminMonitorCloseChan chan struct{}
	msgHandleCloseChan    chan struct{}

	pingFailCount int
	sendFailCount int
}

func newCometClient(_hub *CometHub, _conn *websocket.Conn, msgChanSize int, _bizId, _bizType, channelId, page string) *CometClient {
	return &CometClient{
		hub:  _hub,
		conn: _conn,

		bizId:     _bizId,
		bizType:   _bizType,
		channelId: channelId,
		page:      page,

		send:    make(chan []byte, msgChanSize),
		receive: make(chan []byte, msgChanSize),

		closeChan:             make(chan struct{}),
		adminMonitorCloseChan: make(chan struct{}),
		msgHandleCloseChan:    make(chan struct{}),

		pingFailCount: 0,
		sendFailCount: 0,
	}
}

func (client *CometClient) Close() {
	for {
		select {
		case <-client.closeChan:
			client.conn.Close()
		}
	}
}

func (client *CometClient) Receive() {
	defer func() {
		fmt.Println("receive client close")
		client.conn.Close()

		client.hub.unregister <- client
		client.adminMonitorCloseChan <- struct{}{}
		client.msgHandleCloseChan <- struct{}{}
		if err := recover(); err != nil {
		}
	}()

	client.conn.SetReadLimit(maxMessageSize)
	//client.conn.SetReadDeadline(time.Now().Add(pongWait))
	client.conn.SetReadDeadline(time.Now().Add(readTimeout))
	//client.conn.SetReadDeadline(time.Time{})
	//client.conn.SetPongHandler(func(string) error { client.conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	client.conn.SetPongHandler(func(string) error { client.conn.SetReadDeadline(time.Now().Add(readTimeout)); return nil })

	for {
		_, message, err := client.conn.ReadMessage()
		fmt.Printf("[%v]: receiving message: %s\n", time.Now(), string(message)) 
		if err != nil {
			fmt.Printf("socket read err is %+v\n", err)
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway) {
				fmt.Printf("error: %v\n", err)
			}
			break
		}
		message = bytes.TrimSpace(bytes.Replace(message, newline, space, -1))
		client.receive <- message
	}
}

func (client *CometClient) Send() {
	ticker := time.NewTicker(pingPeriod)

	defer func() {
		fmt.Println("send client close")
		ticker.Stop()
		client.conn.Close()
		client.msgHandleCloseChan <- struct{}{}

		if err := recover(); err != nil {
		}
	}()

	for {
		select {
		case message, ok := <-client.send:
			fmt.Printf("message sent to client: page %s, channelId: %s, clientId %s\n", client.page, client.channelId, client.bizId)
			fmt.Printf("message sent to client is %s\n", string(message))
			//if string(message) != "" {
			client.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// The hub closed the channel.
				client.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := client.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				fmt.Printf("Could not get the socket writer with error:%+v\n", err)
				return
			}
			w.Write(message)
			if err := w.Close(); err != nil {
				//client.sendFailCount += 1
				//if client.sendFailCount > 10 {
				return
				//}
			}
			//}
		case <-ticker.C:
			fmt.Printf("constantly send ping message to client: %+v\n", *client)
			client.conn.SetWriteDeadline(time.Now().Add(writeWait))
			//if err := client.conn.WriteMessage(websocket.PingMessage, []byte{}); err != nil {
			if err := client.conn.WriteMessage(websocket.TextMessage, []byte(`{"act": "test"}`)); err != nil {
				fmt.Printf("failed to send the socket ping message, client is %+v\n", *client)
				//client.pingFailCount += 1
				//if client.pingFailCount > 10 {
				//	return
				//}
				return
			}
		}
	}
}

func serveWS(hub *CometHub, w http.ResponseWriter, r *http.Request) {
	fmt.Printf("new connection is coming\n")
	clientId := r.URL.Query().Get("clientId")
	clientType := r.URL.Query().Get("clientType")
	channelId := r.URL.Query().Get("channelId")
	page := r.URL.Query().Get("page")

	if clientId == "" && clientType == "" {
		return
	}

	conn, err := wsupgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Printf("Failed to set websocket upgrade: %+v\n", err)
		return
	}

	client := newCometClient(hub, conn, 1024, clientId, clientType, channelId, page)
	fmt.Println("prepare to register the client")
	client.hub.register <- client
	fmt.Println("finish to register the client")

	go client.Send()
	go client.Close()

	client.Receive()
}
