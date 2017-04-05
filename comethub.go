package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/garyburd/redigo/redis"
	"github.com/orcaman/concurrent-map"
)

const (
	BIZ_TYPE_ADMIN    = "admin"
	BIZ_TYPE_CUSTOMER = "customer"
	BIZ_TYPE_KF       = "kf"
)

type CometHub struct {
	//clients    map[string]*CometClient
	clients    cmap.ConcurrentMap
	register   chan *CometClient
	unregister chan *CometClient
	mutex      *sync.RWMutex
}

func NewCometHub() *CometHub {
	return &CometHub{
		register:   make(chan *CometClient),
		unregister: make(chan *CometClient),
		//clients:    make(map[string]*CometClient),
		clients: cmap.New(),
		mutex:   new(sync.RWMutex),
	}
}

func (h *CometHub) Run() {
	defer func() {
		if err := recover(); err != nil {
		}
	}()
	for {
		select {
		case client := <-h.register:
			fmt.Printf("new client is registering..: %+v\n", *client)
			if err := storeSocketMap(client.bizType, client.bizId, client.channelId, client.page, bindIPAddress); err != nil {
				client.closeChan <- struct{}{}
				continue
			}

			clientKey := fmt.Sprintf("socket:biztype:%s:bizid:%s:channelid:%s:page:%s", client.bizType, client.bizId, client.channelId, client.page)
			//h.mutex.Lock()
			//h.clients[clientKey] = client
			//h.mutex.Unlock()
			h.clients.Set(clientKey, client)

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
							if act, ok := msg["act"]; ok && act == MSG_TYPE_CHAT || act == MSG_TYPE_QUIT_CHAT {
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
			fmt.Println("close the client")
			clientKey := fmt.Sprintf("socket:biztype:%s:bizid:%s:channelid:%s:page:%s", client.bizType, client.bizId, client.channelId, client.page)
			//h.mutex.Lock()
			//if _, ok := h.clients[clientKey]; ok {
			if _, ok := h.clients.Get(clientKey); ok {
				//delete(h.clients, clientKey)
				h.clients.Remove(clientKey)
				close(client.send)
				close(client.receive)
			}
			//h.mutex.Unlock()

			// delete the socket map in the redis
			deleteSocketMap(client.bizType, client.bizId, client.channelId, client.page)
		}
	}
}

func storeSocketMap(bizType, bizId, channelId, page, ip string) error {
	fmt.Printf("store the socket connection: bizType: %s, bizId %s, channelId: %s, page: %s, ip: %s", bizType, bizId, channelId, page, ip)
	c := redisPool.Get()
	defer c.Close()

	if _, err := c.Do("HSET", KEY_SOCKET_LIST, fmt.Sprintf("socket:biztype:%s:bizid:%s:channelid:%s:page:%s", bizType, bizId, channelId, page), ip); err != nil {
		fmt.Println("ERROR: Fail to store the socket map for biz type %s and biz id %s with error: %+v", bizType, bizId, err)
		return errors.New("Fail to store the socket map")
	}

	return nil
}

func deleteSocketMap(bizType, bizId, channelId, page string) error {
	c := redisPool.Get()
	defer c.Close()

	if _, err := c.Do("HDEL", KEY_SOCKET_LIST, fmt.Sprintf("socket:biztype:%s:bizid:%s:channelid:%s:page:%s", bizType, bizId, channelId, page)); err != nil {
		fmt.Println("ERROR: Fail to delete the socket map for biz type %s and biz id %s with error: %+v", bizType, bizId, err)
		return errors.New("Fail to delete the socket map")
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
			monitorMsg := []byte(v.Data)
			select {
			case client.send <- monitorMsg:
			default:
				return
			}
		case error:
			fmt.Println("ERROR: subscribing the admin online monitor with error: %+v", v)
			return
		}
	}
}
