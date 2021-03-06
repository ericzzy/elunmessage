package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"time"

	"github.com/garyburd/redigo/redis"
)

const (
	MSG_TYPE_KEFU_ONLINE     = "客服上线"
	MSG_TYPE_CUSTOMER_QUEUE  = "排队"
	MSG_TYPE_CUSTOMER_IN     = "客户进入"
	MSG_TYPE_CUSTOMER_LIST   = "客户列表"
	MSG_TYPE_MESSAGE_LIST    = "消息列表"
	MSG_TYPE_KF_LIST         = "客服列表"
	MSG_TYPE_RECEPTION       = "接待"
	MSG_TYPE_MESSAGE         = "消息"
	MSG_TYPE_CHAT            = "聊天"
	MSG_TYPE_SWITCH_CUSTOMER = "打开会话"
	MSG_TYPE_KEFU_OFFLINE    = "客服下线"
	MSG_TYPE_QUIT_CHAT       = "退出"
	MSG_TYPE_ADMIN_ONLINE    = "管理员上线"

	STATUS_ONLINE  = "online"
	STATUS_OFFLINE = "offline"

	SEND_MSG_TYPE_API  = "api"
	SEND_MSG_TYPE_PUSH = "push"

	DATA_TYPE_THREAD_LIST    = "thread_list"
	DATA_TYPE_CURRENT_MSG    = "current_msg"
	DATA_TYPE_ALL_MSG        = "all_msg"
	DATA_TYPE_CURRENT_THREAD = "current_thread"

	KF_PUSH_DATA = "kf_push_data"
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
		return HandleKefuOnlineOfflineMessage(message, STATUS_ONLINE)
	case MSG_TYPE_CUSTOMER_QUEUE:
		return HandleCustomerQueueMessage(message)
	case MSG_TYPE_RECEPTION:
		return HandleReceptionMessage(message)
	case MSG_TYPE_CHAT:
		return HandleChatMessage(message)
	case MSG_TYPE_SWITCH_CUSTOMER:
		return HandleSwitchCustomerMessage(message)
	case MSG_TYPE_KEFU_OFFLINE:
		return HandleKefuOnlineOfflineMessage(message, STATUS_OFFLINE)
	case MSG_TYPE_QUIT_CHAT:
		return HandleQuitChatMessage(message)
	case MSG_TYPE_ADMIN_ONLINE:
		return HandleAdminOnlineMessage(message)
	}

	return nil
}

func HandleKefuOnlineOfflineMessage(message map[string]interface{}, status string) error {
	defer func() {
		if err := recover(); err != nil {
			fmt.Printf("recover from the error: %+v\n", err)
		}
	}()
	act := message[FIELD_ACT]
	// delete the act field
	delete(message, FIELD_ACT)
	message["status"] = status

	kfId := ""
	if _kfId, ok := message[ID_KF]; !ok {
		return nil
	} else {
		kfId = fmt.Sprintf("%v", _kfId)
	}

	if kfId == "" {
		return nil
	}

	c := redisPool.Get()
	defer c.Close()

	fmt.Printf("succeed to get the redis connection: %+v", c)

	existKf, err := c.Do("HGET", KEY_KFLIST, kfId)
	if err != nil {
		fmt.Printf("ERROR: Fail to get the exist kf with error: %+v\n", err)
		return errors.New("Fail to get the exist kf")
	}

	if existKf != nil {
		var existKfMap map[string]interface{}
		if err := json.Unmarshal(existKf.([]byte), &existKfMap); err != nil {
			return errors.New("Could not unmarshal the kf data")
		}

		kfInfo := existKfMap["kfinfo"].(map[string]interface{})
		for k, v := range message {
			if k != "kfinfo" {
				existKfMap[k] = v
				continue
			}
			newKfInfo := v.(map[string]interface{})
			for _k, _v := range newKfInfo {
				kfInfo[_k] = _v
			}
			existKfMap[k] = kfInfo
		}

		message = existKfMap
	}
	// marshal the kefu info to string
	msgBytes, _ := json.Marshal(message)
	msg := string(msgBytes)

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

	kfListPublish := map[string]interface{}{FIELD_ACT: act, "kfslist": kflist}
	kfListPublishBytes, _ := json.Marshal(kfListPublish)

	adminPushDataInterface, err := c.Do("GET", "admin_push_data")
	if err != nil {
		fmt.Printf("ERROR: Fail to get the admin push data with error: %+v\n", err)
		return errors.New("Fail to get the admin push data")
	}

	var adminPushData map[string][]string
	if err := json.Unmarshal(adminPushDataInterface.([]byte), &adminPushData); err != nil {
		fmt.Printf("WARN: Fail to unmarshal the admin push data with error: %+v\n", err)
		return nil
	}

	kfListPages := adminPushData["kf_list"]
	if kfListPages == nil || len(kfListPages) == 0 {
		return nil
	}

	for _, page := range kfListPages {
		// publish the kf list to admin monitor
		if _, err := c.Do("PUBLISH", CHANNEL_ADMIN_ONLINE_MONITOR+page, string(kfListPublishBytes)); err != nil {
			fmt.Println("ERROR: Fail to publish the kf list with error: %+v", err)
			return nil
		}
	}

	return nil
}

func HandleCustomerQueueMessage(message map[string]interface{}) error {
	defer func() {
		if err := recover(); err != nil {
			fmt.Printf("recover from the error: %+v\n", err)
		}
	}()
	delete(message, FIELD_ACT)

	kfId, channelId, threadId, err := preGetKeyId(message)
	if err != nil {
		return err
	}
	chatMessage, ok := message["message"]
	if !ok {
		return errors.New("message was not provided")
	}
	delete(message, "message")

	kfPushDataInterface := message["kf_push_data"]
	if kfPushDataInterface == nil {
		return errors.New("kf_push_data was not provided")
	}

	var kfPushData map[string][]string
	kfPushDataBytes, _ := json.Marshal(kfPushDataInterface)
	if err := json.Unmarshal(kfPushDataBytes, &kfPushData); err != nil {
		fmt.Println("ERROR: kf_push_data format is incorrect")
		return errors.New("kf_push_data format is incorrect")
	}

	threadKey := fmt.Sprintf("threads:kfid:%s:channelid:%s", kfId, channelId)
	threadInfoBytes, _ := json.Marshal(message)

	c := redisPool.Get()
	defer c.Close()

	fmt.Printf("succeed to get the redis connection: %+v", c)

	if _, err := c.Do("HSET", threadKey, threadId, string(threadInfoBytes)); err != nil {
		fmt.Printf("ERROR: Fail to store the thread info with error: %+v\n", err)
		return errors.New("Fail to store the thread info")
	}

	// store the message
	chatMsgBytes, _ := json.Marshal(chatMessage)
	if _, err := c.Do("RPUSH", fmt.Sprintf("messages:kfid:%s:channelid:%s:threadid:%s", kfId, channelId, threadId), string(chatMsgBytes)); err != nil {
		fmt.Printf("Warn: Fail to store the customer queue message with error: %+v\n", err)
		return errors.New("Fail to store the customer queue message")
	}

	// get all the threads
	threadListMsgBytes, err := getAllThreads(c, threadKey, kfId, channelId)
	if err != nil {
		return err
	}

	//cometHub.mutex.RLock()
	//defer cometHub.mutex.RUnlock()

	socketIPMap := make(map[string]interface{})
	pushData(c, socketIPMap, kfPushData[DATA_TYPE_THREAD_LIST], BIZ_TYPE_KF, kfId, "", threadListMsgBytes)

	// handle the current thread
	message["act"] = MSG_TYPE_CUSTOMER_IN
	currentThreadInfoBytes, _ := json.Marshal(message)
	pushData(c, socketIPMap, kfPushData[DATA_TYPE_CURRENT_THREAD], BIZ_TYPE_KF, kfId, "", currentThreadInfoBytes)

	currentMsg := map[string]interface{}{FIELD_ACT: MSG_TYPE_MESSAGE, "kf_id": kfId, "channel_id": channelId, "threadid": threadId, "message": chatMessage}
	currentMsgBytes, _ := json.Marshal(currentMsg)
	fmt.Printf("push current message to customer\n")
	sendMsgTypeInterface := message["sendmsg_type"]
	pushCurrentMsgToCustomer(c, message, sendMsgTypeInterface, channelId, currentMsgBytes, currentMsg, socketIPMap, "kf")

	adminPushDataInterface, err := c.Do("GET", "admin_push_data")
	if err != nil {
		fmt.Printf("ERROR: Fail to get the admin push data with error: %+v\n", err)
		return errors.New("Fail to get the admin push data")
	}

	var adminPushData map[string][]string
	if err := json.Unmarshal(adminPushDataInterface.([]byte), &adminPushData); err != nil {
		fmt.Printf("WARN: Fail to unmarshal the admin push data with error: %+v\n", err)
		return nil
	}

	currentThreadPages := adminPushData["current_thread"]
	threadListPages := adminPushData["thread_list"]

	if currentThreadPages != nil && len(currentThreadPages) > 0 {
		// broadcat the current thread info to admin
		for _, page := range currentThreadPages {
			if _, err := c.Do("PUBLISH", CHANNEL_ADMIN_ONLINE_MONITOR+page, string(currentThreadInfoBytes)); err != nil {
				fmt.Printf("ERROR: Fail to publish the current thread info to admin online monitor  with error: %+v\n", err)
				return nil
			}
		}
	}

	if threadListPages != nil && len(threadListPages) > 0 {
		for _, page := range threadListPages {
			if _, err := c.Do("PUBLISH", CHANNEL_ADMIN_ONLINE_MONITOR+page, string(threadListMsgBytes)); err != nil {
				fmt.Printf("ERROR: Fail to publish the thread list to admin online monitor  with error: %+v\n", err)
				return nil
			}
		}
	}

	return nil
}

func HandleReceptionMessage(message map[string]interface{}) error {
	defer func() {
		if err := recover(); err != nil {
			fmt.Printf("recover from the error: %+v\n", err)
		}
	}()
	delete(message, FIELD_ACT)

	kfId, channelId, threadId, err := preGetKeyId(message)
	if err != nil {
		return err
	}
	chatMessage, ok := message["message"]
	if !ok {
		return errors.New("message was not provided")
	}
	delete(message, "message")

	c := redisPool.Get()
	defer c.Close()

	fmt.Printf("succeed to get the redis connection: %+v", c)

	threadKey := fmt.Sprintf("threads:kfid:%s:channelid:%s", kfId, channelId)
	sendMsgTypeInterface, _, kfPushData, err := preGetThreadConfigAndMerge(c, message, threadKey, threadId)
	if err != nil {
		return err
	}

	threadInfoBytes, _ := json.Marshal(message)
	if _, err := c.Do("HSET", threadKey, threadId, string(threadInfoBytes)); err != nil {
		fmt.Println("ERROR: Fail to store the thread info with error: %+v", err)
		return errors.New("Fail to store the thread info")
	}

	// store the message
	chatMsgBytes, _ := json.Marshal(chatMessage)
	if _, err := c.Do("RPUSH", fmt.Sprintf("messages:kfid:%s:channelid:%s:threadid:%s", kfId, channelId, threadId), string(chatMsgBytes)); err != nil {
		fmt.Println("Warn: Fail to store the customer queue message with error: %+v", err)
		return errors.New("Fail to store the customer queue message")
	}

	// get all the threads
	threadListMsgBytes, err := getAllThreads(c, threadKey, kfId, channelId)
	if err != nil {
		return err
	}
	//cometHub.mutex.RLock()
	//defer cometHub.mutex.RUnlock()

	socketIPMap := make(map[string]interface{})
	pushData(c, socketIPMap, kfPushData[DATA_TYPE_THREAD_LIST], BIZ_TYPE_KF, kfId, "", threadListMsgBytes)

	message["act"] = MSG_TYPE_RECEPTION
	currentThreadInfoBytes, _ := json.Marshal(message)
	pushData(c, socketIPMap, kfPushData[DATA_TYPE_CURRENT_THREAD], BIZ_TYPE_KF, kfId, "", currentThreadInfoBytes)

	var kfIdInterface interface{} = kfId
	if _kfId, err := strconv.Atoi(kfId); err == nil {
		kfIdInterface = _kfId
	}

	var channelIdInterface interface{} = channelId
	if _channelId, err := strconv.Atoi(channelId); err == nil {
		channelIdInterface = _channelId
	}

	currentMsg := map[string]interface{}{FIELD_ACT: MSG_TYPE_MESSAGE, "kf_id": kfIdInterface, "channel_id": channelIdInterface, "threadid": threadId, "message": chatMessage}
	currentMsgBytes, _ := json.Marshal(currentMsg)
	fmt.Printf("Reception: kf_push_data is %+v\n", kfPushData)
	pushData(c, socketIPMap, kfPushData[DATA_TYPE_CURRENT_MSG], BIZ_TYPE_KF, kfId, "", currentMsgBytes)

	// send or push the message via api or socket
	// get the customer id
	fmt.Printf("push current message to customer\n")
	pushCurrentMsgToCustomer(c, message, sendMsgTypeInterface, channelId, currentMsgBytes, currentMsg, socketIPMap, "")

	adminPushDataInterface, err := c.Do("GET", "admin_push_data")
	if err != nil {
		fmt.Printf("ERROR: Fail to get the admin push data with error: %+v\n", err)
		return errors.New("Fail to get the admin push data")
	}

	var adminPushData map[string][]string
	if err := json.Unmarshal(adminPushDataInterface.([]byte), &adminPushData); err != nil {
		fmt.Printf("WARN: Fail to unmarshal the admin push data with error: %+v\n", err)
		return nil
	}

	currentThreadPages := adminPushData["current_thread"]
	threadListPages := adminPushData["thread_list"]

	if currentThreadPages != nil && len(currentThreadPages) > 0 {
		// broadcat the current thread info to admin
		for _, page := range currentThreadPages {
			if _, err := c.Do("PUBLISH", CHANNEL_ADMIN_ONLINE_MONITOR+page, string(currentThreadInfoBytes)); err != nil {
				fmt.Printf("ERROR: Fail to publish the current thread info to admin online monitor  with error: %+v\n", err)
				return nil
			}
		}
	}

	if threadListPages != nil && len(threadListPages) > 0 {
		for _, page := range threadListPages {
			if _, err := c.Do("PUBLISH", CHANNEL_ADMIN_ONLINE_MONITOR+page, string(threadListMsgBytes)); err != nil {
				fmt.Printf("ERROR: Fail to publish the thread list to admin online monitor  with error: %+v\n", err)
				return nil
			}
		}
	}

	return nil
}

func HandleChatMessage(message map[string]interface{}) error {
	defer func() {
		if err := recover(); err != nil {
			fmt.Printf("recover from the error: %+v\n", err)
		}
	}()
	delete(message, FIELD_ACT)

	kfId, channelId, threadId, err := preGetKeyId(message)
	if err != nil {
		return err
	}
	chatMessage, ok := message["message"]
	if !ok {
		return errors.New("message was not provided")
	}
	bizType := message["biz_type"]
	delete(message, "message")
	delete(message, "biz_type")

	c := redisPool.Get()
	defer c.Close()

	fmt.Printf("succeed to get the redis connection: %+v", c)

	// update the thread info
	threadKey := fmt.Sprintf("threads:kfid:%s:channelid:%s", kfId, channelId)
	sendMsgTypeInterface, _, kfPushData, err := preGetThreadConfigAndMerge(c, message, threadKey, threadId)
	if err != nil {
		return err
	}

	threadInfoBytes, _ := json.Marshal(message)
	if _, err := c.Do("HSET", threadKey, threadId, string(threadInfoBytes)); err != nil {
		fmt.Println("ERROR: Fail to store the thread info with error: %+v", err)
		return errors.New("Fail to store the thread info")
	}

	// store the message
	chatMsgBytes, _ := json.Marshal(chatMessage)
	if _, err := c.Do("RPUSH", fmt.Sprintf("messages:kfid:%s:channelid:%s:threadid:%s", kfId, channelId, threadId), string(chatMsgBytes)); err != nil {
		fmt.Println("Warn: Fail to store the customer queue message with error: %+v", err)
		return errors.New("Fail to store the customer queue message")
	}

	// get all the threads
	threadListMsgBytes, err := getAllThreads(c, threadKey, kfId, channelId)
	if err != nil {
		return err
	}
	//cometHub.mutex.RLock()
	//defer cometHub.mutex.RUnlock()
	socketIPMap := make(map[string]interface{})
	pushData(c, socketIPMap, kfPushData[DATA_TYPE_THREAD_LIST], BIZ_TYPE_KF, kfId, "", threadListMsgBytes)

	var kfIdInterface interface{} = kfId
	if _kfId, err := strconv.Atoi(kfId); err == nil {
		kfIdInterface = _kfId
	}

	var channelIdInterface interface{} = channelId
	if _channelId, err := strconv.Atoi(channelId); err == nil {
		channelIdInterface = _channelId
	}

	currentMsg := map[string]interface{}{FIELD_ACT: MSG_TYPE_MESSAGE, "kf_id": kfIdInterface, "channel_id": channelIdInterface, "threadid": threadId, "message": chatMessage}
	currentMsgBytes, _ := json.Marshal(currentMsg)
	pushData(c, socketIPMap, kfPushData[DATA_TYPE_CURRENT_MSG], BIZ_TYPE_KF, kfId, "", currentMsgBytes)

	//if bizType == BIZ_TYPE_KF {
	// send or push the message via api or socket
	// get the customer id
	pushCurrentMsgToCustomer(c, message, sendMsgTypeInterface, channelId, currentMsgBytes, currentMsg, socketIPMap, bizType.(string))
	//}
	return nil
}

func HandleSwitchCustomerMessage(message map[string]interface{}) error {
	defer func() {
		if err := recover(); err != nil {
			fmt.Printf("recover from the error: %+v\n", err)
		}
	}()

	delete(message, FIELD_ACT)

	kfId, channelId, threadId, err := preGetKeyId(message)
	if err != nil {
		return err
	}

	c := redisPool.Get()
	defer c.Close()

	fmt.Printf("succeed to get the redis connection: %+v", c)

	threadKey := fmt.Sprintf("threads:kfid:%s:channelid:%s", kfId, channelId)
	_, _, kfPushData, err := preGetThreadConfigAndMerge(c, message, threadKey, threadId)
	if err != nil {
		return err
	}

	threadInfoBytes, _ := json.Marshal(message)
	if _, err := c.Do("HSET", threadKey, threadId, string(threadInfoBytes)); err != nil {
		fmt.Println("ERROR: Fail to store the thread info with error: %+v", err)
		return errors.New("Fail to store the thread info")
	}

	threadListMsgBytes, err := getAllThreads(c, threadKey, kfId, channelId)
	if err != nil {
		return err
	}

	//cometHub.mutex.RLock()
	//defer cometHub.mutex.RUnlock()
	socketIPMap := make(map[string]interface{})
	fmt.Printf("push thread list message: %+v\n", string(threadListMsgBytes))
	pushData(c, socketIPMap, kfPushData[DATA_TYPE_THREAD_LIST], BIZ_TYPE_KF, kfId, "", threadListMsgBytes)

	// get all the messages for the current thread
	msgs, err := c.Do("LRANGE", fmt.Sprintf("messages:kfid:%s:channelid:%s:threadid:%s", kfId, channelId, threadId), 0, -1)
	if err != nil {
		fmt.Printf("ERROR: Fail to get the messages with error: %+v", err)
		return errors.New("Fail to get the messages")
	}

	if msgs == nil {
		return nil
	}

	msgList := make([]map[string]interface{}, len(msgs.([]interface{})))
	for index, msg := range msgs.([]interface{}) {
		var _msg map[string]interface{}
		json.Unmarshal(msg.([]byte), &_msg)
		msgList[index] = _msg
	}

	var kfIdInterface interface{} = kfId
	if _kfId, err := strconv.Atoi(kfId); err == nil {
		kfIdInterface = _kfId
	}

	var channelIdInterface interface{} = channelId
	if _channelId, err := strconv.Atoi(channelId); err == nil {
		channelIdInterface = _channelId
	}

	msgListMessage := map[string]interface{}{FIELD_ACT: MSG_TYPE_MESSAGE_LIST, "kf_id": kfIdInterface, "channel_id": channelIdInterface, "threadid": threadId, "messages": msgList}
	msgListMessageBytes, _ := json.Marshal(msgListMessage)

	pushData(c, socketIPMap, kfPushData[DATA_TYPE_ALL_MSG], BIZ_TYPE_KF, kfId, "", msgListMessageBytes)

	return nil
}

func HandleQuitChatMessage(message map[string]interface{}) error {
	fmt.Printf("The quit chat message is %+v\n", message)
	defer func() {
		if err := recover(); err != nil {
			fmt.Printf("recover from the error: %+v\n", err)
		}
	}()
	delete(message, FIELD_ACT)

	kfId, channelId, threadId, err := preGetKeyId(message)
	if err != nil {
		return err
	}

	chatMessage, ok := message["message"]
	if !ok {
		return errors.New("message was not provided")
	}
	bizType := message["biz_type"]
	delete(message, "message")
	delete(message, "biz_type")

	c := redisPool.Get()
	defer c.Close()

	fmt.Printf("succeed to get the redis connection: %+v", c)

	threadKey := fmt.Sprintf("threads:kfid:%s:channelid:%s", kfId, channelId)
	sendMsgTypeInterface, saveMsgAPIInterface, kfPushData, err := preGetThreadConfigAndMerge(c, message, threadKey, threadId)
	if err != nil {
		fmt.Printf("preGetThreadConfigAndMerge err is %v\n", err)
		return err
	}

	// delete the current thread
	if _, err := c.Do("HDEL", threadKey, threadId); err != nil {
		fmt.Println("ERROR: Fail to delete the thread info with error: %+v", err)
		return errors.New("Fail to delete the thread info")
	}
	fmt.Printf("Succeed to delete the thread %s: %s\n", threadKey, threadId)

	// store the message
	chatMsgBytes, _ := json.Marshal(chatMessage)
	if _, err := c.Do("RPUSH", fmt.Sprintf("messages:kfid:%s:channelid:%s:threadid:%s", kfId, channelId, threadId), string(chatMsgBytes)); err != nil {
		fmt.Println("Warn: Fail to store the customer queue message with error: %+v", err)
		return errors.New("Fail to store the customer queue message")
	}

	threadListMsgBytes, err := getAllThreads(c, threadKey, kfId, channelId)
	if err != nil {
		return err
	}

	//cometHub.mutex.RLock()
	//defer cometHub.mutex.RUnlock()
	socketIPMap := make(map[string]interface{})
	pushData(c, socketIPMap, kfPushData[DATA_TYPE_THREAD_LIST], BIZ_TYPE_KF, kfId, "", threadListMsgBytes)

	// get all the messages for the current thread
	msgs, err := c.Do("LRANGE", fmt.Sprintf("messages:kfid:%s:channelid:%s:threadid:%s", kfId, channelId, threadId), 0, -1)
	if err != nil {
		fmt.Printf("ERROR: Fail to get the messages with error: %+v", err)
		return errors.New("Fail to get the messages")
	}
	var kfIdInterface interface{} = kfId
	if _kfId, err := strconv.Atoi(kfId); err == nil {
		kfIdInterface = _kfId
	}

	var channelIdInterface interface{} = channelId
	if _channelId, err := strconv.Atoi(channelId); err == nil {
		channelIdInterface = _channelId
	}

	if msgs != nil {
		msgList := make([]map[string]interface{}, len(msgs.([]interface{})))
		for index, msg := range msgs.([]interface{}) {
			var _msg map[string]interface{}
			json.Unmarshal(msg.([]byte), &_msg)
			msgList[index] = _msg
		}
		msgListMessage := map[string]interface{}{FIELD_ACT: MSG_TYPE_MESSAGE, "kf_id": kfIdInterface, "channel_id": channelIdInterface, "threadid": threadId, "messages": msgList}
		if saveMsgAPIInterface != nil {
			saveMsgAPI, ok := saveMsgAPIInterface.(string)
			if ok && saveMsgAPI != "" {
				go MakeHttpRequest(POST, saveMsgAPI, msgListMessage, nil)
			}
		}
	}

	// Todo: delete the thread and messages

	// push the current message to the counterpart
	currentMsg := map[string]interface{}{FIELD_ACT: MSG_TYPE_MESSAGE, "kf_id": kfIdInterface, "channel_id": channelIdInterface, "threadid": threadId, "message": chatMessage}
	currentMsgBytes, _ := json.Marshal(currentMsg)

	currentMsgPages := kfPushData[DATA_TYPE_CURRENT_MSG]
	pushData(c, socketIPMap, currentMsgPages, BIZ_TYPE_KF, kfId, "", currentMsgBytes)
	pushCurrentMsgToCustomer(c, message, sendMsgTypeInterface, channelId, currentMsgBytes, currentMsg, socketIPMap, bizType.(string))
	return nil
}

func HandleAdminOnlineMessage(message map[string]interface{}) error {
	fmt.Printf("admin oneline is coming\n")
	delete(message, "act")
	fmt.Printf("[%+v] admin online message is %v\n", time.Now(), message)

	adminPushData, ok := message["admin_push_data"]
	if !ok {
		return nil
	}

	adminPushDataBytes, _ := json.Marshal(adminPushData)

	c := redisPool.Get()
	defer c.Close()

	if _, err := c.Do("SET", "admin_push_data", string(adminPushDataBytes)); err != nil {
		fmt.Println("ERROR: Fail to store the admin push data with error: %+v", err)
		return errors.New("Fail to store the admin push data")
	}

	return nil
}

func preGetKeyId(message map[string]interface{}) (kfId, channelId, threadId string, retErr error) {
	if _kfId, ok := message["kf_id"]; !ok {
		retErr = errors.New("kf_id was not provided")
		return
	} else {
		kfId = fmt.Sprintf("%v", _kfId)
	}

	if _channelId, ok := message["channel_id"]; !ok {
		retErr = errors.New("channel_id was not provided")
		return
	} else {
		channelId = fmt.Sprintf("%v", _channelId)
	}

	if _threadId, ok := message["threadid"]; !ok {
		retErr = errors.New("threadid was not provided")
		return
	} else {
		threadId = fmt.Sprintf("%v", _threadId)
	}

	if kfId == "" || channelId == "" || threadId == "" {
		retErr = errors.New("kf_id, channel_id or threadid was not provided")
		return
	}

	return
}

func preGetThreadConfigAndMerge(c redis.Conn, message map[string]interface{}, threadKey, threadId string) (sendMsgTypeInterface, saveMsgAPIInterface interface{}, kfPushData map[string][]string, retErr error) {
	sendMsgTypeInterface = message["sendmsg_type"]
	saveMsgAPIInterface = message["savemsg_api"]

	// merge the thread info
	existThreadInfo, err := c.Do("HGET", threadKey, threadId)
	if err != nil {
		fmt.Printf("ERROR: Fail to get the thread info with error: %+v\n", err)
		retErr = errors.New("Fail to get the thread info")
		return
	}

	var kfPushDataObj interface{}
	if existThreadInfo != nil {
		var existThread map[string]interface{}
		if err := json.Unmarshal(existThreadInfo.([]byte), &existThread); err != nil {
			fmt.Printf("ERROR: could not parse the exsiting thread info\n")
			retErr = errors.New("Could not parse the existing thread info")
			return
		}

		customer, ok := existThread["customer"]
		if ok {
			message["customer"] = customer
		}

		kfPushDataObj = existThread[KF_PUSH_DATA]
		if kfPushDataObj != nil {
			message[KF_PUSH_DATA] = kfPushDataObj
		}

		sendMsgTypeInterface = existThread["sendmsg_type"]
		if sendMsgTypeInterface != nil {
			message["sendmsg_type"] = sendMsgTypeInterface
		}
		saveMsgAPIInterface = existThread["savemsg_api"]
		if saveMsgAPIInterface != nil {
			message["savemsg_api"] = saveMsgAPIInterface
		}

		newThreadInfo, _ := message["threadinfo"]

		threadinfoInterface := existThread["threadinfo"]
		if threadinfoInterface != nil && reflect.ValueOf(threadinfoInterface).Kind() == reflect.Map {
			threadinfoMap := threadinfoInterface.(map[string]interface{})
			if newThreadInfo != nil && reflect.ValueOf(newThreadInfo).Kind() == reflect.Map {
				// merge the new thread info
				newThreadInfoMap := newThreadInfo.(map[string]interface{})
				for k, v := range newThreadInfoMap {
					threadinfoMap[k] = v
				}
			}
			message["threadinfo"] = threadinfoMap
		}
	}

	if kfPushDataObj == nil {
		kfPushDataObj = message[KF_PUSH_DATA]
		if kfPushDataObj == nil {
			retErr = errors.New("kf_push_data was not provided")
			return
		}
	}

	kfPushDataBytes, _ := json.Marshal(kfPushDataObj)
	if err := json.Unmarshal(kfPushDataBytes, &kfPushData); err != nil {
		fmt.Println("ERROR: kf_push_data format is incorrect")
		retErr = errors.New("kf_push_data format is incorrect")
		return
	}

	return
}

func getAllThreads(c redis.Conn, threadKey, kfId, channelId string) ([]byte, error) {
	// get all the threads
	threadlist := make([]map[string]interface{}, 0)
	threadsInterface, err := c.Do("HGETALL", threadKey)
	if err != nil {
		fmt.Println("ERROR: Fail to get the threads list with error: %+v", err)
		return nil, errors.New("Fail to get the threads list")
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

	var kfIdInterface interface{} = kfId
	if _kfId, err := strconv.Atoi(kfId); err == nil {
		kfIdInterface = _kfId
	}

	var channelIdInterface interface{} = channelId
	if _channelId, err := strconv.Atoi(channelId); err == nil {
		channelIdInterface = _channelId
	}

	// push all the threads to the kf
	threadListMsg := map[string]interface{}{FIELD_ACT: MSG_TYPE_CUSTOMER_LIST, "kf_id": kfIdInterface, "channel_id": channelIdInterface, "threads": threadlist}
	threadListMsgBytes, _ := json.Marshal(threadListMsg)
	return threadListMsgBytes, nil
}

func pushData(c redis.Conn, socketIPMap map[string]interface{}, pages []string, bizType, bizId, channelId string, message []byte) {
	fmt.Println("entering push data")
	if socketIPMap == nil {
		socketIPMap = make(map[string]interface{})
	}
	if pages == nil || message == nil || len(message) == 0 {
		fmt.Println("no message need to push")
		return
	}

	for _, page := range pages {
		clientKey := fmt.Sprintf("socket:biztype:%s:bizid:%s:channelid:%s:page:%s", bizType, bizId, channelId, page)
		fmt.Printf("pushing data to %s\n", clientKey)
		socketIPVal, ok := socketIPMap[clientKey]
		if !ok {
			// get the kf's ip
			var err error
			socketIPVal, err = c.Do("HGET", KEY_SOCKET_LIST, clientKey)
			if err != nil {
				fmt.Printf("ERROR: Fail to get the socket ip  with error: %+v\n", err)
				continue
			}
		}

		if socketIPVal == nil {
			continue
		}

		socketIPMap[clientKey] = socketIPVal

		// push the current thread info to the corresponding kf
		socketIP := string(socketIPVal.([]byte))
		if socketIP != "" && socketIP == bindIPAddress {
			fmt.Println("push message locally")
			//cometClient := cometHub.clients[clientKey]
			var cometClient *CometClient
			_cometClient, ok := cometHub.clients.Get(clientKey)
			if !ok {
				continue
			}
			cometClient = _cometClient.(*CometClient)
			fmt.Printf("prepare to push message to %s\n", clientKey)
			if cometClient != nil {
				fmt.Println("start to push message")
				go func() {
					defer func() {
						recover()
					}()
					fmt.Println("hello world")
					cometClient.send <- message
				}()
			}
		} else {
			fmt.Println("send message via rpc")
			go PushRPCMessage(socketIP, [][]byte{message}, bizType, bizId, channelId, page)
		}
	}
}

func pushCurrentMsgToCustomer(c redis.Conn, message map[string]interface{}, sendMsgTypeInterface interface{}, channelId string, currentMsgBytes []byte, currentMsg map[string]interface{}, socketIPMap map[string]interface{}, customerBizType string) {
	threadInfo, ok := message["threadinfo"]
	if !ok {
		fmt.Printf("Error: thread info does not exist\n")
		return
	}

	if reflect.ValueOf(threadInfo).Kind() != reflect.Map {
		fmt.Printf("Error: thred info is not map\n")
		return
	}

	threadInfoMap := threadInfo.(map[string]interface{})
	customerIdObj, ok := threadInfoMap["customer_id"]
	if !ok {
		fmt.Printf("Error: customer_id is not in the thread\n")
		return
	}

	customerId := fmt.Sprintf("%v", customerIdObj)
	if customerId == "" {
		fmt.Printf("INFO: customer id is empty so no mesage is sent or push\n")
		return
	}

	// how to push the message
	if sendMsgTypeInterface == nil || sendMsgTypeInterface == "" {
		fmt.Printf("push message to customer\n")
		pushData(c, socketIPMap, []string{""}, BIZ_TYPE_CUSTOMER, customerId, channelId, currentMsgBytes)
	} else {
		if customerBizType != "" && customerBizType != BIZ_TYPE_KF {
			return
		}
		_sendMsgType, ok := sendMsgTypeInterface.(string)
		if ok {
			fmt.Printf("send message to customer via %s\n", _sendMsgType)
			go MakeHttpRequest(POST, _sendMsgType, currentMsg, nil)
		}
	}
	fmt.Println("exit from pushCurrentMsgToCustomer")
}
