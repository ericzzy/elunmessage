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
var redisBindAddress string
var redisPwd string

func init() {
	bindServerIP := flag.String("bind", "192.168.1.8:9000", "API服务绑定IP和端口")
	redisServerIP := flag.String("redisAddr", "192.168.1.8:6379", "Redis服务IP和端口")
	redisServerPwd := flag.String("redisPwd", "foobared", "Redis服务密码")

	flag.Parse()

	if bindServerIP == nil {
		fmt.Println("没有提供API服务绑定IP和端口")
		os.Exit(1)
	}

	if redisServerIP == nil {
		fmt.Println("没有提供Redis服务绑定IP和端口")
		os.Exit(1)
	}

	bindIPAddress = *bindServerIP
	redisBindAddress = *redisServerIP

	if redisServerPwd != nil {
		redisPwd = *redisServerPwd
	}
}

func main() {
	redisPool = redis.NewPool(func() (redis.Conn, error) {
		var c redis.Conn
		var err error
		if redisPwd != "" {
			c, err = redis.Dial("tcp", redisBindAddress, redis.DialPassword(redisPwd))
		} else {
			c, err = redis.Dial("tcp", redisBindAddress)
		}
		if err != nil {
			return nil, err
		}
		return c, err
	}, 100)

	if redisPool == nil {
		fmt.Println("Redis服务不可用")
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
