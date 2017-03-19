package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
)

const (
	MSG_TYPE_KEFU_ONLINE    = "客服上线"
	MSG_TYPE_CUSTOMER_QUEUE = "排队"
	MSG_TYPE_CUSTOMER_IN    = "客户进入"
	MSG_TYPE_CUSTOMER_LIST  = "客户列表"
	MSG_TYPE_KF_LIST        = "客服列表"
	MSG_TYPE_RECEPTION      = "接待"
	MSG_TYPE_MESSAGE        = "消息"
	MSG_TYPE_CHAT           = "聊天"

	STATUS_ONLINE = "online"

	SEND_MSG_TYPE_API  = "api"
	SEND_MSG_TYPE_PUSH = "push"
)

func HandleMessage(message map[string]interface{}) error {
	_act, ok := message[FIELD_ACT]
	if !ok {
		return nil
	}

	act, ok := _act.(string)
	if !ok {
		return nil
	}

	switch act {
	case MSG_TYPE_KEFU_ONLINE:
		return HandleKefuOnlineMessage(message)
	case MSG_TYPE_CUSTOMER_QUEUE:
		return HandleCustomerQueueMessage(message)
	case MSG_TYPE_RECEPTION:
		return HandleReceptionMessage(message)
	case MSG_TYPE_CHAT:
		return HandleChatMessage(message)
	}

	return nil
}

func HandleKefuOnlineMessage(message map[string]interface{}) error {
	// delete the act field
	delete(message, FIELD_ACT)
	message["status"] = STATUS_ONLINE

	kfId := ""
	if _kfId, ok := message[ID_KF]; !ok {
		return nil
	} else {
		kfId = _kfId.(string)
	}

	if kfId == "" {
		return nil
	}

	// marshal the kefu info to string
	msgBytes, _ := json.Marshal(message)
	msg := string(msgBytes)

	c := redisPool.Get()
	defer c.Close()

	// insert or update the keifu info and status
	if _, err := c.Do("HSET", KEY_KFLIST, kfId, msg); err != nil {
		fmt.Println("ERROR: Fail to store the kf info with error: %+v", err)
		return errors.New("Fail to store the kf info")
	}

	// get all kf list
	kflist := make([]map[string]interface{}, 0)
	vInterface, err := c.Do("HGETALL", KEY_KFLIST)
	if err != nil {
		fmt.Println("ERROR: Fail to get the kf list with error: %+v", err)
		return errors.New("Fail to get the kf list")
	}
	if vInterface != nil {
		v := vInterface.([]interface{})
		kflist = make([]map[string]interface{}, len(v)/2)
		index := 0
		for i := 1; i < len(v); i += 2 {
			var kf map[string]interface{}
			json.Unmarshal(v[i].([]byte), &kf)
			kflist[index] = kf
			index++
		}
	}

	kfListPublish := map[string]interface{}{FIELD_ACT: MSG_TYPE_KF_LIST, "kfslist": kflist}
	kfListPublishBytes, _ := json.Marshal(kfListPublish)

	// publish the kf list to admin monitor
	fmt.Printf("publish the online kf %s to admin \n", string(kfListPublishBytes))
	if _, err := c.Do("PUBLISH", CHANNEL_ADMIN_ONLINE_MONITOR, string(kfListPublishBytes)); err != nil {
		fmt.Println("ERROR: Fail to publish the kf list with error: %+v", err)
		return nil
	}

	return nil
}

func HandleCustomerQueueMessage(message map[string]interface{}) error {
	delete(message, FIELD_ACT)

	kfId := message["kf_id"]
	channelId := message["channel_id"]
	threadId := message["thread_id"]
	chatMessage := message["message"]

	threadKey := fmt.Sprintf("threads:kfid:%s:channelid:%s", kfId.(string), channelId.(string))

	delete(message, "message")

	threadInfoBytes, _ := json.Marshal(message)

	c := redisPool.Get()
	defer c.Close()

	if _, err := c.Do("HSET", threadKey, threadId, string(threadInfoBytes)); err != nil {
		fmt.Printf("ERROR: Fail to store the thread info with error: %+v\n", err)
		return errors.New("Fail to store the thread info")
	}

	// store the message
	chatMsgBytes, _ := json.Marshal(chatMessage)
	if _, err := c.Do("RPUSH", fmt.Sprintf("messages:kfid:%s:channelId:%s:threadId:%s", kfId.(string), channelId.(string), threadId.(string)), string(chatMsgBytes)); err != nil {
		fmt.Printf("Warn: Fail to store the customer queue message with error: %+v\n", err)
		return errors.New("Fail to store the customer queue message")
	}

	// get all the threads
	threadlist := make([]map[string]interface{}, 0)
	threadsInterface, err := c.Do("HGETALL", threadKey)
	if err != nil {
		fmt.Printf("ERROR: Fail to get the threads list with error: %+v\n", err)
		return errors.New("Fail to get the threads list")
	}

	if threadsInterface != nil {
		v := threadsInterface.([]interface{})
		threadlist = make([]map[string]interface{}, len(v)/2)
		index := 0
		for i := 1; i < len(v); i += 2 {
			var thread map[string]interface{}
			json.Unmarshal(v[i].([]byte), &thread)
			threadlist[index] = thread
			index++
		}
	}

	// get the kf's ip
	socketIPVal, err := c.Do("HGET", KEY_SOCKET_LIST, fmt.Sprintf("socket:biztype:%s:bizid:%s", BIZ_TYPE_KF, kfId.(string)))
	if err != nil {
		fmt.Printf("ERROR: Fail to get the socket ip  with error: %+v\n", err)
	}
	// push all the threads to the kf
	threadListMsg := map[string]interface{}{FIELD_ACT: MSG_TYPE_CUSTOMER_LIST, "kf_id": kfId.(string), "channel_id": channelId.(string), "threads": threadlist}
	threadListMsgBytes, _ := json.Marshal(threadListMsg)

	// push the current thread info to the corresponding kf
	if socketIPVal != nil {
		socketIP := string(socketIPVal.([]byte))
		if socketIP != "" && socketIP == bindIPAddress {
			clientKey := fmt.Sprintf("socket:biztype:%s:bizid:%s", BIZ_TYPE_KF, kfId.(string))
			cometHub.mutex.RLock()
			cometClient := cometHub.clients[clientKey]
			cometHub.mutex.RUnlock()
			if cometClient != nil {
				go func() {
					cometClient.send <- threadListMsgBytes
					cometClient.send <- threadInfoBytes
				}()
			}
		} else {
			PushRPCMessage(socketIP, [][]byte{threadListMsgBytes, threadInfoBytes}, BIZ_TYPE_KF, kfId.(string))
		}
	}

	// broadcat the current thread info to admin
	if _, err := c.Do("PUBLISH", CHANNEL_ADMIN_ONLINE_MONITOR, string(threadInfoBytes)); err != nil {
		fmt.Printf("ERROR: Fail to publish the current thread info to admin online monitor  with error: %+v\n", err)
		return nil
	}

	return nil
}

func HandleReceptionMessage(message map[string]interface{}) error {
	delete(message, FIELD_ACT)

	kfId := message["kf_id"]
	channelId := message["channel_id"]
	threadId := message["thread_id"]
	chatMessage := message["message"]

	sendMsgType := message["sendmsg_type"]

	// update the thread info
	threadKey := fmt.Sprintf("threads:kfid:%s:channelid:%s", kfId.(string), channelId.(string))

	delete(message, "message")

	c := redisPool.Get()
	defer c.Close()
	// merge the thread info
	existThreadInfo, err := c.Do("HGET", threadKey, threadId.(string))
	if err != nil {
		fmt.Printf("ERROR: Fail to get the thread info with error: %+v\n", err)
		return errors.New("Fail to get the thread info")
	}

	if existThreadInfo != nil {
		var existThread map[string]interface{}
		if err := json.Unmarshal(existThreadInfo.([]byte), &existThread); err != nil {
			fmt.Printf("ERROR: could not parse the exsiting thread info\n")
			return errors.New("Could not parse the existing thread info")
		}

		customer, ok := existThread["customer"]
		if ok {
			message["customer"] = customer
		}
	}
	threadInfoBytes, _ := json.Marshal(message)

	if _, err := c.Do("HSET", threadKey, threadId, string(threadInfoBytes)); err != nil {
		fmt.Println("ERROR: Fail to store the thread info with error: %+v", err)
		return errors.New("Fail to store the thread info")
	}

	// store the message
	chatMsgBytes, _ := json.Marshal(chatMessage)
	if _, err := c.Do("RPUSH", fmt.Sprintf("messages:kfid:%s:channelId:%s:threadId:%s", kfId.(string), channelId.(string), threadId.(string)), string(chatMsgBytes)); err != nil {
		fmt.Println("Warn: Fail to store the customer queue message with error: %+v", err)
		return errors.New("Fail to store the customer queue message")
	}

	// get all the threads
	threadlist := make([]map[string]interface{}, 0)
	threadsInterface, err := c.Do("HGETALL", threadKey)
	if err != nil {
		fmt.Println("ERROR: Fail to get the threads list with error: %+v", err)
		return errors.New("Fail to get the threads list")
	}
	if threadsInterface != nil {
		v := threadsInterface.([]interface{})
		threadlist = make([]map[string]interface{}, len(v)/2)
		index := 0
		for i := 1; i < len(v); i += 2 {
			var thread map[string]interface{}
			json.Unmarshal(v[i].([]byte), &thread)
			threadlist[index] = thread
			index++
		}
	}

	// get the kf's ip
	socketIPVal, err := c.Do("HGET", KEY_SOCKET_LIST, fmt.Sprintf("socket:biztype:%s:bizid:%s", BIZ_TYPE_KF, kfId.(string)))
	if err != nil {
		fmt.Println("ERROR: Fail to get the socket ip  with error: %+v", err)
	}
	// push all the threads to the kf
	threadListMsg := map[string]interface{}{FIELD_ACT: MSG_TYPE_CUSTOMER_LIST, "kf_id": kfId.(string), "channel_id": channelId.(string), "threads": threadlist}
	threadListMsgBytes, _ := json.Marshal(threadListMsg)

	currentMsg := map[string]interface{}{FIELD_ACT: MSG_TYPE_MESSAGE, "kf_id": kfId.(string), "channel_id": channelId.(string), "thread_id": threadId.(string), "message": chatMessage}
	currentMsgBytes, _ := json.Marshal(currentMsg)

	// push the current thread info to the corresponding kf
	if socketIPVal != nil {
		socketIP := string(socketIPVal.([]byte))
		if socketIP != "" && socketIP == bindIPAddress {
			clientKey := fmt.Sprintf("socket:biztype:%s:bizid:%s", BIZ_TYPE_KF, kfId.(string))
			cometHub.mutex.RLock()
			cometClient := cometHub.clients[clientKey]
			cometHub.mutex.RUnlock()
			if cometClient != nil {
				go func() {
					cometClient.send <- threadListMsgBytes
					cometClient.send <- threadInfoBytes
					cometClient.send <- currentMsgBytes
				}()
			}
		} else {
			PushRPCMessage(socketIP, [][]byte{threadListMsgBytes, threadInfoBytes, currentMsgBytes}, BIZ_TYPE_KF, kfId.(string))
		}
	}

	// broadcat the current thread info to admin
	if _, err := c.Do("PUBLISH", CHANNEL_ADMIN_ONLINE_MONITOR, string(threadInfoBytes)); err != nil {
		fmt.Println("ERROR: Fail to publish the current thread info to admin online monitor  with error: %+v", err)
		return nil
	}

	// send or push the message via api or socket
	// get the customer id
	threadInfo, ok := message["threadinfo"]
	if !ok {
		return nil
	}

	if reflect.TypeOf(threadInfo).String() != "map[string]interface {}" {
		return nil
	}

	threadInfoMap := threadInfo.(map[string]interface{})
	customerId, ok := threadInfoMap["customer_id"]
	if !ok || customerId == "" {
		return nil
	}

	switch sendMsgType {
	case SEND_MSG_TYPE_PUSH:
		socketIPVal, err := c.Do("HGET", KEY_SOCKET_LIST, fmt.Sprintf("socket:biztype:%s:bizid:%s", BIZ_TYPE_CUSTOMER, customerId.(string)))
		if err != nil {
			fmt.Printf("Could not get the socket ip for biz type %s, biz id %s with error: %+v", BIZ_TYPE_CUSTOMER, customerId.(string), err)
			return err
		}
		if socketIPVal != nil {
			socketIP := string(socketIPVal.([]byte))
			if socketIP == "" {
				return nil
			}
			if socketIP == bindIPAddress {
				clientKey := fmt.Sprintf("socket:biztype:%s:bizid:%s", BIZ_TYPE_CUSTOMER, customerId.(string))
				cometHub.mutex.RLock()
				cometClient := cometHub.clients[clientKey]
				cometHub.mutex.RUnlock()
				if cometClient != nil {
					go func() {
						cometClient.send <- currentMsgBytes
					}()
				}
			} else {
				PushRPCMessage(socketIP, [][]byte{currentMsgBytes}, BIZ_TYPE_CUSTOMER, customerId.(string))
			}
		}
	case SEND_MSG_TYPE_API:
		//TODO: send the message to the business api
	}

	return nil
}

func HandleChatMessage(message map[string]interface{}) error {
	delete(message, FIELD_ACT)

	kfId := message["kf_id"]
	channelId := message["channel_id"]
	threadId := message["thread_id"]
	chatMessage := message["message"]

	sendMsgType := message["sendmsg_type"]
	bizType := message["biz_type"]

	// update the thread info
	threadKey := fmt.Sprintf("threads:kfid:%s:channelid:%s", kfId.(string), channelId.(string))

	delete(message, "message")
	delete(message, "biz_type")

	c := redisPool.Get()
	defer c.Close()
	// merge the thread info
	existThreadInfo, err := c.Do("HGET", threadKey, threadId.(string))
	if err != nil {
		fmt.Printf("ERROR: Fail to get the thread info with error: %+v\n", err)
		return errors.New("Fail to get the thread info")
	}

	if existThreadInfo != nil {
		var existThread map[string]interface{}
		if err := json.Unmarshal(existThreadInfo.([]byte), &existThread); err != nil {
			fmt.Printf("ERROR: could not parse the exsiting thread info\n")
			return errors.New("Could not parse the existing thread info")
		}

		customer, ok := existThread["customer"]
		if ok {
			message["customer"] = customer
		}
	}
	threadInfoBytes, _ := json.Marshal(message)

	if _, err := c.Do("HSET", threadKey, threadId, string(threadInfoBytes)); err != nil {
		fmt.Println("ERROR: Fail to store the thread info with error: %+v", err)
		return errors.New("Fail to store the thread info")
	}

	// store the message
	chatMsgBytes, _ := json.Marshal(chatMessage)
	if _, err := c.Do("RPUSH", fmt.Sprintf("messages:kfid:%s:channelId:%s:threadId:%s", kfId.(string), channelId.(string), threadId.(string)), string(chatMsgBytes)); err != nil {
		fmt.Println("Warn: Fail to store the customer queue message with error: %+v", err)
		return errors.New("Fail to store the customer queue message")
	}

	// get all the threads
	threadlist := make([]map[string]interface{}, 0)
	threadsInterface, err := c.Do("HGETALL", threadKey)
	if err != nil {
		fmt.Println("ERROR: Fail to get the threads list with error: %+v", err)
		return errors.New("Fail to get the threads list")
	}
	if threadsInterface != nil {
		v := threadsInterface.([]interface{})
		threadlist = make([]map[string]interface{}, len(v)/2)
		index := 0
		for i := 1; i < len(v); i += 2 {
			var thread map[string]interface{}
			json.Unmarshal(v[i].([]byte), &thread)
			threadlist[index] = thread
			index++
		}
	}

	// get the kf's ip
	socketIPVal, err := c.Do("HGET", KEY_SOCKET_LIST, fmt.Sprintf("socket:biztype:%s:bizid:%s", BIZ_TYPE_KF, kfId.(string)))
	if err != nil {
		fmt.Println("ERROR: Fail to get the socket ip  with error: %+v", err)
	}
	// push all the threads to the kf
	threadListMsg := map[string]interface{}{FIELD_ACT: MSG_TYPE_CUSTOMER_LIST, "kf_id": kfId.(string), "channel_id": channelId.(string), "threads": threadlist}
	threadListMsgBytes, _ := json.Marshal(threadListMsg)

	currentMsg := map[string]interface{}{FIELD_ACT: MSG_TYPE_MESSAGE, "kf_id": kfId.(string), "channel_id": channelId.(string), "thread_id": threadId.(string), "message": chatMessage}
	currentMsgBytes, _ := json.Marshal(currentMsg)

	// push the current thread info to the corresponding kf
	if socketIPVal != nil {
		socketIP := string(socketIPVal.([]byte))
		if socketIP != "" && socketIP == bindIPAddress {
			clientKey := fmt.Sprintf("socket:biztype:%s:bizid:%s", BIZ_TYPE_KF, kfId.(string))
			cometHub.mutex.RLock()
			cometClient := cometHub.clients[clientKey]
			cometHub.mutex.RUnlock()
			if cometClient != nil {
				go func() {
					cometClient.send <- threadListMsgBytes
					cometClient.send <- currentMsgBytes
				}()
			}
		} else {
			PushRPCMessage(socketIP, [][]byte{threadListMsgBytes, currentMsgBytes}, BIZ_TYPE_KF, kfId.(string))
		}
	}

	if bizType == BIZ_TYPE_KF {
		// send or push the message via api or socket
		// get the customer id
		threadInfo, ok := message["threadinfo"]
		if !ok {
			return nil
		}

		if reflect.TypeOf(threadInfo).String() != "map[string]interface {}" {
			return nil
		}

		threadInfoMap := threadInfo.(map[string]interface{})
		customerId, ok := threadInfoMap["customer_id"]
		if !ok || customerId == "" {
			return nil
		}

		switch sendMsgType {
		case SEND_MSG_TYPE_PUSH:
			socketIPVal, err := c.Do("HGET", KEY_SOCKET_LIST, fmt.Sprintf("socket:biztype:%s:bizid:%s", BIZ_TYPE_CUSTOMER, customerId.(string)))
			if err != nil {
				fmt.Printf("Could not get the socket ip for biz type %s, biz id %s with error: %+v", BIZ_TYPE_CUSTOMER, customerId.(string), err)
				return err
			}
			if socketIPVal != nil {
				socketIP := string(socketIPVal.([]byte))
				if socketIP == "" {
					return nil
				}
				if socketIP == bindIPAddress {
					clientKey := fmt.Sprintf("socket:biztype:%s:bizid:%s", BIZ_TYPE_CUSTOMER, customerId.(string))
					cometHub.mutex.RLock()
					cometClient := cometHub.clients[clientKey]
					cometHub.mutex.RUnlock()
					if cometClient != nil {
						go func() {
							cometClient.send <- currentMsgBytes
						}()
					}
				} else {
					PushRPCMessage(socketIP, [][]byte{currentMsgBytes}, BIZ_TYPE_CUSTOMER, customerId.(string))
				}
			}
		case SEND_MSG_TYPE_API:
			//TODO: send the message to the business api
		}
	}
	return nil
}
