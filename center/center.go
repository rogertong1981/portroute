package main

import (
	"flag"
	"fmt"
	"math/rand"
	"net"
	"runtime"
	"strconv"
	"sync"
	"time"

	"portroute/common"
)

type instance struct {
	key         string
	tunnelKey   string
	forwardConn net.Conn
	proxyConn   net.Conn
	exitChan    chan bool
}

type tunnel struct {
	key         string
	forwardConn net.Conn
	proxyConn   net.Conn
	instances   map[string]*instance
}

var tunnelConns = make(map[string]*tunnel)

func createKey() int {
	var m sync.Mutex
	rand.Seed(time.Now().UnixNano())
	m.Lock()
	defer m.Unlock()
	s := 0
	for {
		s = rand.Intn(999999)
		if s < 100000 {
			s = s + 100000
		}
		str := strconv.Itoa(s)
		if _, ok := tunnelConns[str]; !ok {
			break
		}
	}
	return s
}

func getTunnel(key *string, conn net.Conn) *tunnel {
	var m sync.Mutex
	m.Lock()
	defer m.Unlock()
	if len(*key)<3 || *key == "000000" {
		*key = strconv.Itoa(createKey())
		common.WriteByte(conn, common.SetTunnelKey)
		common.WriteString(conn, *key)
	}
	tun, ok := tunnelConns[*key]
	if !ok {
		tun = &tunnel{}
		tun.key = *key
		tun.instances = make(map[string]*instance)
		tunnelConns[*key] = tun
	}
	return tun
}

func kickForwardTunnel(tun *tunnel) {
	for k, v := range tun.instances {
		if v.proxyConn != nil {
			v.proxyConn.Close()
			v.proxyConn = nil
		}

		if v.forwardConn != nil {
			v.forwardConn.Close()
			v.forwardConn = nil
		}

		delete(tun.instances, k)
	}

	if tun.forwardConn != nil {
		common.WriteByte(tun.forwardConn, common.KickForwardTunnelConn)
		tun.forwardConn.Close()
		fmt.Printf("Forward-Tunnel[%s]已被踢下线\n", tun.key)
		tun.forwardConn = nil
	}
}

func kickProxyTunnel(tun *tunnel) {
	for k, v := range tun.instances {
		if v.proxyConn != nil {
			v.proxyConn.Close()
			v.proxyConn = nil
		}

		if v.forwardConn != nil {
			v.forwardConn.Close()
			v.forwardConn = nil
		}

		delete(tun.instances, k)
	}

	if tun.proxyConn != nil {
		common.WriteByte(tun.proxyConn, common.KickProxyTunnelConn)
		tun.proxyConn.Close()
		fmt.Printf("Proxy-Tunnel[%s]已被踢下线\n", tun.key)
		tun.proxyConn = nil
	}
}

func sendNotify(tunConn net.Conn, msg string) {
	common.WriteByte(tunConn, common.NotifyMessage)
	common.WriteString(tunConn, msg)
}

func createProxyTunnelConn(conn net.Conn) {
	defer func() {
		if err := recover(); err != nil{
			conn.Close()
			fmt.Println(err)
		}
	}()

	go common.Ping(conn)
	tunnelKey, _ := common.ReadString(conn)
	tun := getTunnel(&tunnelKey, conn)
	if tun.proxyConn != conn {
		kickProxyTunnel(tun)
	}
	tun.proxyConn = conn
	fmt.Printf("Proxy-Tunnel[%s][%s]已接入成功\n", tun.key, tun.proxyConn.RemoteAddr())
	sendNotify(conn, fmt.Sprintf("Proxy-Tunnel[%s]已成功连接到中央服务器", tun.key))
	if tun.forwardConn == nil {
		sendNotify(conn, fmt.Sprintf("对应的Forward-Tunnel[%s]还没有连接到服务端", tun.key))
	} else {
		sendNotify(conn, fmt.Sprintf("对应的Forward-Tunnel[%s]在服务端已接入", tun.key))
		sendNotify(tun.forwardConn, fmt.Sprintf("对应的Proxy-Tunnel[%s]在服务端已接入", tun.key))
	}

	for {
		cmd, err := common.ReadByte(conn)
		if err != nil {
			fmt.Printf("Proxy-Tunnel[%s][%s]已断开连接\n", tun.key, conn.RemoteAddr())
			conn.Close()
			if tun.proxyConn == conn {
				tun.proxyConn = nil
			}
			for k, v := range tun.instances {
				if v.forwardConn != nil {
					v.forwardConn.Close()
					v.forwardConn = nil
				}
				if v.proxyConn != nil {
					v.proxyConn.Close()
					v.proxyConn = nil
				}
				delete(tun.instances, k)
			}
			break
		}
		switch cmd {
		case common.FwNotifyMessage:
			sendFwNotifyMessage(conn)
		}
		runtime.Gosched()
	}
}

func createForwardTunnelConn(conn net.Conn) {
	defer func() {
		if err := recover(); err != nil{
			conn.Close()
			fmt.Println(err)
		}
	}()

	go common.Ping(conn)
	tunnelKey, err1 := common.ReadString(conn)
	if err1 != nil {
		conn.Close()
		return 
	}

	tun := getTunnel(&tunnelKey, conn)
	if tun.forwardConn != conn {
		kickForwardTunnel(tun)
	}
	tun.forwardConn = conn
	fmt.Printf("Forward-Tunnel[%s][%s]已接入成功\n", tun.key, tun.forwardConn.RemoteAddr())
	if tun.key != "000000" {
		sendNotify(conn, fmt.Sprintf("Forward-Tunnel[%s]已成功连接到中央服务器", tun.key))
		if tun.proxyConn != nil {
			sendNotify(conn, fmt.Sprintf("对应的Proxy-Tunnel[%s]在服务端已接入", tun.key))
			sendNotify(tun.proxyConn, fmt.Sprintf("对应的Forward-Tunnel[%s]在服务端已接入", tun.key))
		}
	}

	for {
		cmd, err := common.ReadByte(conn)
		if err != nil {
			fmt.Printf("Forward-Tunnel[%s][%s]已断开连接\n", tun.key, conn.RemoteAddr())
			conn.Close()
			if tun.forwardConn == conn {
				tun.forwardConn = nil
			}
			for k, v := range tun.instances {
				if v.forwardConn != nil {
					v.forwardConn.Close()
					v.forwardConn = nil
				}
				if v.proxyConn != nil {
					v.proxyConn.Close()
					v.proxyConn = nil
				}
				delete(tun.instances, k)
			}
			if tun.proxyConn == nil {
				delete(tunnelConns, tunnelKey)
			}
			break
		}
		switch cmd {
		case common.FwNotifyMessage:
			sendFwNotifyMessage(conn)
		}
		runtime.Gosched()
	}
}

func createProxyInstanceConn(conn net.Conn) {
	defer func() {
		if err := recover(); err != nil{
			conn.Close()
			fmt.Println(err)
		}
	}()

	tunKey, _ := common.ReadString(conn)
	insKey, _ := common.ReadString(conn)
	remoteSrv, _ := common.ReadString(conn)

	if tun, ok := tunnelConns[tunKey]; ok {
		if tun.forwardConn == nil {
			notifyMsg := fmt.Sprintf("Instance[%s][%s]正在等待对应的Forward-Tunnel接入中..", insKey, remoteSrv)
			fmt.Println(notifyMsg)
			sendNotify(tun.proxyConn, notifyMsg)
			for tun.forwardConn == nil {
				time.Sleep(time.Second * 1)
			}
		}

		fmt.Printf("Instance[%s][%s]正在建立中转连接\n", insKey, remoteSrv)
		common.WriteByte(tun.forwardConn, common.AddForwardLink)
		common.WriteString(tun.forwardConn, insKey)
		common.WriteString(tun.forwardConn, remoteSrv)

		ins := &instance{}
		ins.tunnelKey = tunKey
		ins.key = insKey
		ins.proxyConn = conn
		ins.exitChan = make(chan bool, 1)
		tun.instances[insKey] = ins
	}
}

func initForwardInstanceLink(conn net.Conn) {
	defer func() {
		if err := recover(); err != nil {
			conn.Close()
			fmt.Println(err)
		}
	}()

	tunKey, _ := common.ReadString(conn)
	insKey, _ := common.ReadString(conn)
	fmt.Printf("Instance[%s]中转连接建立已就绪\n", insKey)
	if tun, ok := tunnelConns[tunKey]; ok {
		if ins, ok1 := tun.instances[insKey]; ok1 {
			ins.forwardConn = conn
			defer func() {
				if ins.proxyConn != nil {
					ins.proxyConn.Close()
				}
				if ins.forwardConn != nil {
					ins.forwardConn.Close()
				}
				fmt.Printf("Instance[%s]中转连接建立已关闭\n", insKey)
				delete(tun.instances, insKey)
			}()
			common.WriteByte(ins.proxyConn, common.ForwardLinkSuccess)
			go common.IoCopy(ins.proxyConn, ins.forwardConn, ins.exitChan)
			go common.IoCopy(ins.forwardConn, ins.proxyConn, ins.exitChan)
			<-ins.exitChan

		}
	}
}

func sendFwNotifyMessage(conn net.Conn) {
	tunKey, _ := common.ReadString(conn)
	msg, _ := common.ReadString(conn)
	if tun, ok := tunnelConns[tunKey]; ok {
		if tun.proxyConn != nil {
			sendNotify(tun.proxyConn, msg)
		}
	}
}

func server(portStr string) {
	lis, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%v", portStr))
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("copyright by rogertong(tongbin@lonntec.com)")
	fmt.Printf("PortRouter中央服务已启动[%v]\n", lis.Addr())
	defer lis.Close()

	for {
		conn, err := lis.Accept()
		if err != nil {
			fmt.Println(err)
			if conn!=nil {
				conn.Close()
			}
			continue
		}

		defer func() {
			common.PrintError()
			if conn != nil {
				conn.Close()
			}
		}()

		fmt.Printf("检测到来自[%v]新的连接请求\n", conn.RemoteAddr())

		go func() {
			cmd, _ := common.ReadByte(conn)
			switch cmd {
			case common.ForwareTunnelConn:
				go createForwardTunnelConn(conn)
			case common.ProxyTunnelConn:
				go createProxyTunnelConn(conn)
			case common.ProxyInstanceConn:
				go createProxyInstanceConn(conn)
			case common.ForwardInstanceConn:
				go initForwardInstanceLink(conn)
			default:
				fmt.Printf("检测到异常连接指令[%v],连接[%v]将被断开\n", cmd, conn.RemoteAddr())
				conn.Close()
			}
		}()
	}
}

func main() {
	var portStr string
	flag.StringVar(&portStr, "p", "3600", "-p=<port> 指定中央服务的端口")
	flag.Parse()

	server(portStr)
}
