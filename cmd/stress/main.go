// WS 压测脚本——验证 Hub 在大量连接下的 goroutine 泄漏和内存表现。
//
// 用法：
//
//	go run ./cmd/stress/ -n 500          # 500 个并发连接
//	go run ./cmd/stress/ -n 500 -dur 60  # 500 连接持续 60 秒
//	go run ./cmd/stress/ -n 1000 -ramp 50ms  # 每 50ms 建一个新连接
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

var (
	numConns     = flag.Int("n", 100, "并发 WS 连接数")
	durationSec  = flag.Int("dur", 30, "保持连接时长（秒）")
	rampInterval = flag.Duration("ramp", 10*time.Millisecond, "建连间隔")
	serverURL    = flag.String("url", "ws://localhost:7777/v1/ws", "WS 服务地址")
	token        = flag.String("token", "test", "JWT token")
)

func main() {
	flag.Parse()
	fmt.Printf("=== WS 压测 ===\n")
	fmt.Printf("目标: %s\n", *serverURL)
	fmt.Printf("连接数: %d, 持续: %ds, 建连间隔: %v\n\n", *numConns, *durationSec, *rampInterval)

	var (
		connected int64
		failed    int64
		received  int64
		wg        sync.WaitGroup
	)

	// 先查下基线——压测前 goroutine 数
	printGoroutineCount("压测前")

	// 逐步建立连接——模拟真实用户行为，不是瞬间爆炸
	start := time.Now()
	for i := 0; i < *numConns; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			url := fmt.Sprintf("%s?token=%s", *serverURL, *token)
			conn, _, err := websocket.DefaultDialer.Dial(url, nil)
			if err != nil {
				atomic.AddInt64(&failed, 1)
				return
			}
			atomic.AddInt64(&connected, 1)
			defer conn.Close()

			// 启动读协程——模拟真实客户端接收广播
			done := make(chan struct{})
			go func() {
				defer close(done)
				for {
					_, _, err := conn.ReadMessage()
					if err != nil {
						return
					}
					atomic.AddInt64(&received, 1)
				}
			}()

			// 发送一条消息模拟用户行为
			conn.WriteMessage(websocket.TextMessage, []byte("压测消息"))

			// 保持连接
			select {
			case <-time.After(time.Duration(*durationSec) * time.Second):
			case <-done:
			}
		}(i)

		time.Sleep(*rampInterval)
	}

	wg.Wait()
	elapsed := time.Since(start)

	printGoroutineCount("压测后（连接已断开）")

	fmt.Printf("\n=== 结果 ===\n")
	fmt.Printf("目标连接: %d\n", *numConns)
	fmt.Printf("成功连接: %d\n", atomic.LoadInt64(&connected))
	fmt.Printf("失败连接: %d\n", atomic.LoadInt64(&failed))
	fmt.Printf("收到消息: %d\n", atomic.LoadInt64(&received))
	fmt.Printf("耗时:     %v\n", elapsed)
	fmt.Printf("建连速率: %.0f conn/s\n", float64(*numConns)/elapsed.Seconds())

	// 用 /debug/pprof/ 验证
	fmt.Printf("\npprof 面板: http://localhost:6060/debug/pprof/\n")
	fmt.Printf("Goroutine:  http://localhost:6060/debug/pprof/goroutine?debug=1\n")
}

func printGoroutineCount(label string) {
	resp, err := http.Get("http://localhost:6060/debug/pprof/goroutine?debug=1")
	if err != nil {
		log.Printf("[%s] 无法访问 pprof: %v", label, err)
		return
	}
	defer resp.Body.Close()

	// 简单统计行数 ≈ goroutine 数
	buf := make([]byte, 64*1024)
	n, _ := resp.Body.Read(buf)
	lines := 0
	for _, b := range buf[:n] {
		if b == '\n' {
			lines++
		}
	}
	// 去掉 header 行（约 2-3 行）
	goroutines := lines - 3
	if goroutines < 0 {
		goroutines = 0
	}
	fmt.Printf("[%s] goroutine ≈ %d\n", label, goroutines)
}
