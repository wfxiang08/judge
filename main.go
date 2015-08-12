package main

import (
	"flag"
	"fmt"
	"github.com/open-falcon/judge/cron"
	"github.com/open-falcon/judge/g"
	"github.com/open-falcon/judge/http"
	"github.com/open-falcon/judge/rpc"
	"github.com/open-falcon/judge/store"
	"os"
)

func main() {
	cfg := flag.String("c", "cfg.json", "configuration file")
	version := flag.Bool("v", false, "show version")
	flag.Parse()

	if *version {
		fmt.Println(g.VERSION)
		os.Exit(0)
	}

	g.ParseConfig(*cfg)

	g.InitRedisConnPool()

	// 建立和心跳服务器之间的RpcClient
	g.InitHbsClient()

	//HistoryBigMap: 256个元素(作用)
	store.InitHistoryBigMap()

	go http.Start()
	go rpc.Start()

	// 同步策略
	go cron.SyncStrategies()
	go cron.CleanStale()

	select {}
}
