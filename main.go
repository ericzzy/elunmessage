package main

import (
	"flag"
	"fmt"
	"log"
	//"net"
	"net/http"
	//"net/rpc"
	"os"
	"runtime"

	"github.com/garyburd/redigo/redis"
	//	"github.com/gin-gonic/gin"

	"runtime/pprof"
)

var redisPool *redis.Pool
var bindIPAddress string
var cometHub *CometHub
var redisBindAddress string
var redisPwd string

var cpuProfile string

func init() {
	runtime.GOMAXPROCS(runtime.NumCPU() * 2)

	bindServerIP := flag.String("bind", "192.168.1.8:9000", "API服务绑定IP和端口")
	redisServerIP := flag.String("redisAddr", "192.168.1.8:6379", "Redis服务IP和端口")
	redisServerPwd := flag.String("redisPwd", "foobared", "Redis服务密码")
	cpuprofile := flag.String("cpuprofile", "", "write cpu profile to file")

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

	if *cpuprofile != "" {
		cpuProfile = *cpuprofile
	}
}

func main() {
	if cpuProfile != "" {
		log.Println("create the cpu profile log file")
		f, err := os.Create(cpuProfile)
		if err != nil {
			log.Fatal(err)
		}
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}

	redisPool = &redis.Pool{
		MaxIdle:   100,
		MaxActive: 10000,
		Dial: func() (redis.Conn, error) {
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
		},
	}

	if redisPool == nil {
		fmt.Println("Redis服务不可用")
		return
	}
	defer redisPool.Close()

	cometHub = NewCometHub()
	go cometHub.Run()

	/*
		if bindIPAddress != "" {
			pushMsgHandler := &PushMessageHandler{hub: cometHub}
			rpcServer := rpc.NewServer()
			rpcServer.RegisterName("PushMessageHandler", pushMsgHandler)
			rpcServer.HandleHTTP("/rpc", "/rpc/debug")

			//gin.SetMode(gin.ReleaseMode)

			l, e := net.Listen("tcp", bindIPAddress)
			if e != nil {
				log.Fatal("listen error:", e)
			}

			// This statement starts go's http server on
			// socket specified by l.
			fmt.Println("he")
			go http.Serve(l, nil)

		}
	*/

	router := initRoutes(cometHub)
	server := &http.Server{
		Addr:    bindIPAddress,
		Handler: router,
	}
	server.ListenAndServe()
}
