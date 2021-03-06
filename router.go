package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
)

func initRoutes(hub *CometHub) *gin.Engine {
	router := gin.Default()

	v1 := router.Group("/v1")

	{
		v1.GET("/sub", func(c *gin.Context) {
			serveWS(hub, c.Writer, c.Request)
		})

		v1.POST("/messages", handleMsgFromService)

		v1.POST("/pushmsgs", handlePushMsgService)

		// add other handlers here
	}

	/*

		router.LoadHTMLFiles("index.html")
		router.GET("/", func(c *gin.Context) {
			c.HTML(200, "index.html", nil)
		})

		return router
	*/

	return router
}

func handleMsgFromService(c *gin.Context) {
	var msg map[string]interface{}
	if err := c.BindJSON(&msg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": http.StatusBadRequest, "message": "消息不能解析"})
		c.AbortWithError(http.StatusBadRequest, errors.New("消息不能解析"))
		return
	}

	if _, ok := msg["act"]; !ok {
		c.JSON(http.StatusBadRequest, gin.H{"code": http.StatusBadRequest, "message": "act参数缺失"})
		c.AbortWithError(http.StatusBadRequest, errors.New("act参数缺失"))
		return
	}

	if act, ok := msg["act"]; ok && act == MSG_TYPE_CHAT || act == MSG_TYPE_QUIT_CHAT {
		msg["biz_type"] = BIZ_TYPE_CUSTOMER
	}

	msgBytes, _ := json.Marshal(msg)
	fmt.Printf("message received is %v\n", string(msgBytes))

	// process the message
	//if err := HandleMessage(msg); err != nil {
	//c.JSON(http.StatusBadRequest, gin.H{"code": http.StatusInternalServerError, "message": err.Error()})
	//c.AbortWithError(http.StatusInternalServerError, err)
	//return

	//}
	go HandleMessage(msg)

	c.JSON(http.StatusOK, map[string]string{"result": "success"})
}

func handlePushMsgService(c *gin.Context) {
	msg := new(PushMessage)
	if err := c.BindJSON(msg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": http.StatusBadRequest, "message": "消息不能解析"})
		c.AbortWithError(http.StatusBadRequest, errors.New("消息不能解析"))
		return
	}

	go (&PushMessageHandler{hub: cometHub}).Push(msg, nil)

	c.JSON(http.StatusOK, map[string]string{"result": "success"})
}
