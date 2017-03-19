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
			if err := storeSocketMap(client.bizType, client.bizId, client.channelId, bindIPAddress); err != nil {
				client.closeChan <- struct{}{}
				continue
			}

			clientKey := fmt.Sprintf("socket:biztype:%s:bizid:%s:channelid:%s", client.bizType, client.bizId, client.channelId)
			h.clients[clientKey] = client

			if client.bizType == BIZ_TYPE_ADMIN {
				fmt.Printf("listen to the admin monitor")
				go subscribeAdminOnlineMonitor(client)
			}

			go func(c *CometClient) {
				for {
					select {
					case message := <-c.receive:
						var msg map[string]interface{}
						if err := json.Unmarshal(message, &msg); err == nil {
							if act, ok := msg["act"]; ok && act == MSG_TYPE_CHAT {
								msg["biz_type"] = c.bizType
							}
							HandleMessage(msg)
						}
					case <-c.msgHandleCloseChan:
						return
					}
				}
			}(client)
		case client := <-h.unregister:
			clientKey := fmt.Sprintf("socket:biztype:%s:bizid:%s:channelid:%s", client.bizType, client.bizId, client.channelId)
			if _, ok := h.clients[clientKey]; ok {
				delete(h.clients, clientKey)
				close(client.send)
			}
		}
	}
}

func storeSocketMap(bizType, bizId, channelId, ip string) error {
	c := redisPool.Get()
	defer c.Close()

	if _, err := c.Do("HSET", KEY_SOCKET_LIST, fmt.Sprintf("socket:biztype:%s:bizid:%s:channelid:%s", bizType, bizId, channelId), ip); err != nil {
		fmt.Println("ERROR: Fail to store the socket map for biz type %s and biz id %s with error: %+v", bizType, bizId, err)
		return errors.New("Fail to store the socket map")
	}

	return nil
}

func subscribeAdminOnlineMonitor(client *CometClient) {
	c := redisPool.Get()
	defer func() {
		c.Close()
		if err := recover(); err != nil {
			fmt.Printf("error in the subscibe function is %+v\n", err)
		}
	}()

	// subscribe the channel
	psc := redis.PubSubConn{c}
	psc.Subscribe(CHANNEL_ADMIN_ONLINE_MONITOR)
	for {
		switch v := psc.Receive().(type) {
		case redis.Message:
			fmt.Printf("Receive message: %s\n", string([]byte(v.Data)))
			monitorMsg := []byte(v.Data)
			//_, isClose := <-client.send
			//if !isClose {
			client.send <- monitorMsg
			//}
		case error:
			fmt.Println("ERROR: subscribing the admin online monitor with error: %+v", v)
			return
		}

		/*
			select {
			case <-client.adminMonitorCloseChan:
				fmt.Printf("admin monitor close")
				return
			}
		*/
	}
}
