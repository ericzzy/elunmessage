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
	pingPeriod     = (pongWait * 9) / 10
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

	closeChan             chan struct{}
	adminMonitorCloseChan chan struct{}
	msgHandleCloseChan    chan struct{}
}

func newCometClient(_hub *CometHub, _conn *websocket.Conn, msgChanSize int, _bizId, _bizType, channelId string) *CometClient {
	return &CometClient{
		hub:  _hub,
		conn: _conn,

		bizId:     _bizId,
		bizType:   _bizType,
		channelId: channelId,

		send:    make(chan []byte, msgChanSize),
		receive: make(chan []byte, msgChanSize),

		closeChan:             make(chan struct{}),
		adminMonitorCloseChan: make(chan struct{}),
		msgHandleCloseChan:    make(chan struct{}),
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
		client.conn.Close()

		client.hub.unregister <- client
		client.adminMonitorCloseChan <- struct{}{}
		client.msgHandleCloseChan <- struct{}{}
	}()

	client.conn.SetReadLimit(maxMessageSize)
	client.conn.SetReadDeadline(time.Now().Add(pongWait))
	client.conn.SetPongHandler(func(string) error { client.conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })

	for {
		_, message, err := client.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway) {
				fmt.Printf("error: %v\n", err)
			}
			break
		}
		message = bytes.TrimSpace(bytes.Replace(message, newline, space, -1))
		fmt.Printf("receving message: %s", string(message))
		client.receive <- message
	}
}

func (client *CometClient) Send() {
	ticker := time.NewTicker(pingPeriod)

	defer func() {
		ticker.Stop()
		client.conn.Close()

		client.hub.unregister <- client
		client.adminMonitorCloseChan <- struct{}{}
		client.msgHandleCloseChan <- struct{}{}
	}()

	for {
		select {
		case message, ok := <-client.send:
			client.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// The hub closed the channel.
				client.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			fmt.Printf("Push message to socket client: %s\n", string(message))

			w, err := client.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				fmt.Printf("Could not get the socket writer with error:%+v\n", err)
				return
			}
			w.Write(message)
			if err := w.Close(); err != nil {
				return
			}
		case <-ticker.C:
			client.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := client.conn.WriteMessage(websocket.PingMessage, []byte{}); err != nil {
				return
			}
		}
	}
}

func serveWS(hub *CometHub, w http.ResponseWriter, r *http.Request) {
	clientId := r.URL.Query().Get("clientId")
	clientType := r.URL.Query().Get("clientType")
	channelId := r.URL.Query().Get("channelId")

	if clientId == "" || clientType == "" {
		return
	}

	conn, err := wsupgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Printf("Failed to set websocket upgrade: %+v\n", err)
		return
	}

	client := newCometClient(hub, conn, 1024, clientId, clientType, channelId)
	client.hub.register <- client

	go client.Send()

	client.Receive()
}
