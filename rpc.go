// The rpc send out the message via socket
package main

import (
	"fmt"
	//"net/rpc"
)

// server side
type PushMessageHandler struct {
	hub *CometHub
}

type PushMessage struct {
	Message   []byte `json:"message"`
	BizType   string `json:"biz_type"`
	BizId     string `json:"biz_id"`
	ChannelId string `json:"channel_id"`
	Page      string `json:"page"`
}

func (h *PushMessageHandler) Push(message *PushMessage, reply *int) error {
	//*reply = 0
	fmt.Printf("Start to push the message %+v\n", *message)

	if message == nil {
		return nil
	}
	fmt.Printf("Prepare to push the message %+v\n", *message)

	clientKey := fmt.Sprintf("socket:biztype:%s:bizid:%s:channelid:%s:page:%s", message.BizType, message.BizId, message.ChannelId, message.Page)

	//h.hub.mutex.RLock()
	_client, ok := h.hub.clients.Get(clientKey)
	if !ok {
		return nil
	}

	client := _client.(*CometClient)
	//h.hub.mutex.RUnlock()

	if client == nil {
		return nil
	}

	fmt.Printf("Remote to push the message: %s\n", string(message.Message))

	client.send <- message.Message

	return nil
}

// client side
/*
func PushRPCMessage(serverIP string, messages [][]byte, bizType string, bizId string, channelId, page string) {
	client, err := rpc.DialHTTP("tcp", serverIP)
	if err != nil {
		fmt.Println("ERROR: could not connect to the rpc server: %s", serverIP)
		return
	}

	for _, message := range messages {
		pushMessage := PushMessage{
			Message:   message,
			BizType:   bizType,
			BizId:     bizId,
			ChannelId: channelId,
			Page:      page,
		}

		var reply int

		err = client.Call("PushMessageHandler.Push", pushMessage, &reply)
		if err != nil || reply != 0 {
			fmt.Println("ERROR: could not push the message %s to the rpc server with error: %+v", string(message), err)
		}
	}
}
*/

func PushRPCMessage(serverIP string, messages [][]byte, bizType string, bizId string, channelId, page string) {
	for _, message := range messages {
		entity := map[string]interface{}{
			"message":    message,
			"biz_type":   bizType,
			"biz_id":     bizId,
			"channel_id": channelId,
			"page":       page,
		}

		MakeHttpRequest("POST", "http://"+serverIP+"/v1/pushmsgs", entity, nil)
	}
}
