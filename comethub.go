package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/garyburd/redigo/redis"
)

const (
	BIZ_TYPE_ADMIN    = "admin"
	BIZ_TYPE_CUSTOMER = "customer"
	BIZ_TYPE_KF       = "kf"
)

type CometHub struct {
	clients    map[string]*CometClient
	register   chan *CometClient
	unregister chan *CometClient
	mutex      *sync.RWMutex
}

func NewCometHub() *CometHub {
	return &CometHub{
		register:   make(chan *CometClient),
		unregister: make(chan *CometClient),
		clients:    make(map[string]*CometClient),
		mutex:      new(sync.RWMutex),
	}
}

func (h *CometHub) Run() {
	for {
		select {
		case client := <-h.register:
			if err := storeSocketMap(client.bizType, client.bizId, bindIPAddress); err != nil {
				client.closeChan <- struct{}{}
				continue
			}

			clientKey := fmt.Sprintf("socket:biztype:%s:bizid:%s", client.bizType, client.bizId)
			h.clients[clientKey] = client

			if client.bizType == BIZ_TYPE_ADMIN {
				go subscribeAdminOnlineMonitor(client)
			}

			go func(c *CometClient) {
				for {
					select {
					case message := <-c.receive:
						var msg map[string]interface{}
						if err := json.Unmarshal(message, &msg); err == nil {
							HandleMessage(msg)
						}
					case <-c.msgHandleCloseChan:
						return
					}
				}
			}(client)
		case client := <-h.unregister:
			clientKey := fmt.Sprintf("biztype:%s:bizid:%s", client.bizType, client.bizId)
			if _, ok := h.clients[clientKey]; ok {
				delete(h.clients, clientKey)
				close(client.send)
			}
		}
	}
}

func storeSocketMap(bizType, bizId, ip string) error {
	c := redisPool.Get()
	defer c.Close()

	if _, err := c.Do("HSET", KEY_SOCKET_LIST, fmt.Sprintf("socket:biztype:%s:bizid:%s", bizType, bizId), ip); err != nil {
		fmt.Println("ERROR: Fail to store the socket map for biz type %s and biz id %s with error: %+v", bizType, bizId, err)
		return errors.New("Fail to store the socket map")
	}

	return nil
}

func subscribeAdminOnlineMonitor(client *CometClient) {
	// subscribe the channel
	c := redisPool.Get()
	defer c.Close()

	psc := redis.PubSubConn{c}
	psc.Subscribe(CHANNEL_ADMIN_ONLINE_MONITOR)
	for {
		select {
		case <-client.adminMonitorCloseChan:
			return
		}

		switch v := psc.Receive().(type) {
		case redis.Message:
			monitorMsg := []byte(v.Data)
			client.send <- monitorMsg
		case error:
			fmt.Println("ERROR: subscribing the admin online monitor with error: %+v", v)
			return
		}
	}
}
