package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/shirou/gopsutil/v3/net"
)

// Message отправляется в UI
type Msg struct {
	Ts   int64   `json:"ts"`
	Rx   float64 `json:"rx_bps"`
	Tx   float64 `json:"tx_bps"`
	RxKb float64 `json:"rx_kbps"`
	TxKb float64 `json:"tx_kbps"`
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

var clients = struct {
	sync.Mutex
	conns map[*websocket.Conn]bool
}{conns: make(map[*websocket.Conn]bool)}

func wsHandler(w http.ResponseWriter, r *http.Request) {
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	clients.Lock()
	clients.conns[c] = true
	clients.Unlock()

	for {
		if _, _, err := c.NextReader(); err != nil {
			clients.Lock()
			delete(clients.conns, c)
			clients.Unlock()
			c.Close()
			break
		}
	}
}

func broadcast(m Msg) {
	clients.Lock()
	defer clients.Unlock()
	b, _ := json.Marshal(m)
	for c := range clients.conns {
		_ = c.WriteMessage(websocket.TextMessage, b)
	}
}

func httpServe(addr string) {
	http.Handle("/", http.FileServer(http.Dir("./web")))
	http.HandleFunc("/ws", wsHandler)
	log.Printf("HTTP server at %s\n", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}

func collectLoop(interval time.Duration) {
	prev, _ := net.IOCounters(true)
	sumPrev := sumIO(prev)
	t := time.NewTicker(interval)
	for range t.C {
		now, _ := net.IOCounters(true)
		sumNow := sumIO(now)

		deltaRx := float64(sumNow.RxBytes - sumPrev.RxBytes)
		deltaTx := float64(sumNow.TxBytes - sumPrev.TxBytes)

		rxBps := deltaRx / interval.Seconds()
		txBps := deltaTx / interval.Seconds()

		msg := Msg{
			Ts:   time.Now().UnixMilli(),
			Rx:   rxBps,
			Tx:   txBps,
			RxKb: rxBps / 1024.0,
			TxKb: txBps / 1024.0,
		}
		broadcast(msg)
		sumPrev = sumNow
	}
}

type ioSum struct {
	RxBytes uint64
	TxBytes uint64
}

func sumIO(s []net.IOCountersStat) ioSum {
	var r ioSum
	for _, v := range s {
		if strings.HasPrefix(v.Name, "Loopback") || strings.HasPrefix(v.Name, "lo") {
			continue
		}
		r.RxBytes += v.BytesRecv
		r.TxBytes += v.BytesSent
	}
	return r
}

func main() {
	addr := "127.0.0.1:8080"
	go httpServe(addr)

	go collectLoop(500 * time.Millisecond)

	url := "http://" + addr + "/ui.html"
	go func() {
		time.Sleep(time.Second)
		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "windows":
			cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
		case "darwin":
			cmd = exec.Command("open", url)
		default:
			cmd = exec.Command("xdg-open", url)
		}
		_ = cmd.Start()
	}()

	fmt.Println("Server running at", url)
	select {}
}
