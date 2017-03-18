package main

import (
	"flag"
	"fmt"
	"net/http"
	"net/rpc"
	"os"

	"github.com/garyburd/redigo/redis"
	"github.com/gin-gonic/gin"
)

var redisPool *redis.Pool

var bindIPAddress string

var cometHub *CometHub

func init() {
	bindServerIP := flag.String("bind", "192.168.1.8:9000", "服务绑定IP和端口")

	flag.Parse()

	if bindServerIP == nil {
		fmt.Println("没有提供服务绑定IP和端口")
		os.Exit(1)
	}

	bindIPAddress = *bindServerIP
}

func main() {
	redisPool := redis.NewPool(func() (redis.Conn, error) {
		c, err := redis.Dial("tcp", "127.0.0.1:6379", redis.DialPassword("foobared"))
		if err != nil {
			return nil, err
		}
		return c, err
	}, 100)

	if redisPool == nil {
		return
	}
	defer redisPool.Close()

	cometHub := NewCometHub()
	go cometHub.Run()

	pushMsgHandler := &PushMessageHandler{hub: cometHub}
	rpc.Register(pushMsgHandler)
	rpc.HandleHTTP()

	gin.SetMode(gin.ReleaseMode)

	router := initRoutes(cometHub)
	server := &http.Server{
		Addr:    bindIPAddress,
		Handler: router,
	}
	server.ListenAndServe()
}
