package main

import (
	"flag"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"portroute/common"
)

type instance struct {
	key         string
	forwardConn net.Conn //系统建立的中转连接通道
	proxyConn   net.Conn //连接到本地端口的连接
	exitChan    chan bool
}

type linkInfo struct {
	key        string
	serverAddr string
	serverPort int
	listenPort int
}

type listenInfo struct {
	key      string
	linkInfo *linkInfo
	// instances map[string]*instance
}

var tunnelConn net.Conn
var tunnelKey string
var centersrv string

var linkInfos = make(map[string]*linkInfo)
var listenInfos = make(map[string]*listenInfo)
var instanceKey = 100

func getNewInstanceKey(link *listenInfo) string {
	var m sync.Mutex
	m.Lock()
	defer m.Unlock()
	instanceKey = instanceKey + 1
	return fmt.Sprintf("%v-%v", link.key, instanceKey)
}

func getInputTunnelKey() string {
	var str string
	fmt.Print("请输入需要连接的信道标识:")
	fmt.Scanln(&str)
	if len(str) < 6 {
		println("信道标识最少使用6个字符来表示,请您重新输入:")
		return getInputTunnelKey()
	}
	return str
}

func getInputPort(msg string) int {
	var portStr string
	fmt.Print(msg)
	fmt.Scanln(&portStr)
	v, e := strconv.Atoi(portStr)
	if e != nil || v < 1 || v > 65534 {
		return getInputPort("你输入的端口号无效,请重新输入:")
	}
	return v
}

func getInputRemoteServer() {
	println("请输入你需要添加的后端服务器信息")
	var serverAddr string
	var appendNext string
	fmt.Print("服务器地址:")
	fmt.Scanln(&serverAddr)
	serverPort := getInputPort("服务器端口号:")
	listenPort := getInputPort("本地监听端口号:")
	key := fmt.Sprintf("%v-%v", tunnelKey, listenPort)
	if info, ok := linkInfos[key]; !ok {
		var m sync.Mutex
		m.Lock()
		defer m.Unlock()
		info = &linkInfo{}
		info.key = key
		info.listenPort = listenPort
		info.serverAddr = serverAddr
		info.serverPort = serverPort
		linkInfos[key] = info
	}

	fmt.Print("是否还需要添加后端服务器？(y/n)")
	fmt.Scanln(&appendNext)
	if appendNext == "y" || appendNext == "Y" {
		getInputRemoteServer()
	}
}

func getListenInfo(link *linkInfo) *listenInfo {
	var m sync.Mutex
	m.Lock()
	defer m.Unlock()
	if info, ok := listenInfos[link.key]; !ok {
		info = &listenInfo{}
		info.key = link.key
		info.linkInfo = link
		listenInfos[link.key] = info
	}
	return listenInfos[link.key]
}

func acceptProxyInstanceConnect(conn net.Conn, link *listenInfo) {
	// for k, v := range link.instances {
	// 	if v.proxyConn != nil {
	// 		v.proxyConn.Close()
	// 	}
	// 	if v.forwardConn != nil {
	// 		v.forwardConn.Close()
	// 	}
	// 	v.exitChan <- false
	// 	delete(link.instances, k)
	// }

	ins := &instance{}
	ins.key = getNewInstanceKey(link)
	ins.exitChan = make(chan bool, 1)
	ins.proxyConn = conn
	remoteSrv := fmt.Sprintf("%v:%v", link.linkInfo.serverAddr, link.linkInfo.serverPort)
	fmt.Printf("正在建立Proxy-Instance[%v][%v]转发连接...\n", ins.key, remoteSrv)
	fwConn, err := net.Dial("tcp", centersrv)
	if err != nil {
		fmt.Printf("建立Proxy-Instance[%v][%v]转发连接时出错:\n%v\n", ins.key, remoteSrv, err)
		return
	}
	defer func() {
		fwConn.Close()
		conn.Close()
		fmt.Printf("Forward-Instance[%v][%v]连接已关闭:\n", ins.key, remoteSrv)
	}()
	ins.forwardConn = fwConn
	common.WriteByte(fwConn, common.ProxyInstanceConn)
	common.WriteString(fwConn, tunnelKey)
	common.WriteString(fwConn, ins.key)

	common.WriteString(fwConn, remoteSrv)
	cmd, _ := common.ReadByte(fwConn)
	if cmd == common.ForwardLinkSuccess {
		fmt.Printf("Forward-Instance[%v][%v]连接已就绪:\n", ins.key, remoteSrv)
		go common.IoCopy(ins.forwardConn, ins.proxyConn, ins.exitChan)
		go common.IoCopy(ins.proxyConn, ins.forwardConn, ins.exitChan)
		<-ins.exitChan
	}
}

func createListen(info *listenInfo) {
	lis, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%v", info.linkInfo.listenPort))
	if err != nil {
		fmt.Printf("打开本地监听端口[%v]失败：\n%v\n", info.linkInfo.listenPort, err)
		return
	}
	defer lis.Close()

	for {
		conn, err := lis.Accept()
		if err != nil {
			fmt.Println(err)
			continue
		}
		go acceptProxyInstanceConnect(conn, info)
	}
}

func createListens() {
	for _, v := range linkInfos {
		info := getListenInfo(v)
		createListen(info)
	}
}

func connectCenter(centerSrv string) {
	conn, err := net.Dial("tcp", centerSrv)
	if err != nil {
		time.Sleep(time.Second * 3)
		connectCenter(centerSrv)
	}
	defer conn.Close()

	for k := range listenInfos {
		delete(linkInfos, k)
	}

	go createListens()

	tunnelConn = conn
	common.WriteByte(conn, common.ProxyTunnelConn)
	common.WriteString(conn, tunnelKey)
	for {
		cmd, err := common.ReadByte(conn)
		if err != nil {
			fmt.Printf("检测到中央服务器[%s]的连接已经断开，正在尝试重连...\n", centerSrv)
			connectCenter(centerSrv)
		}
		switch cmd {
		case common.NotifyMessage:
			msg, _ := common.ReadString(conn)
			fmt.Println(msg)
		case common.KickProxyTunnelConn:
			fmt.Printf("有新的用户使用信道标示[%s]连接到中央服务器，你已经被踢下线!\n", tunnelKey)
			fmt.Println("程序将在5秒后自动关闭!")
			time.Sleep(time.Second * 5)
			return
		}
	}
}

func main() {
	var tunKey string
	fmt.Println("copyright by rogertong(tongbin@lonntec.com)")
	flag.StringVar(&centersrv, "center", common.DefaultCenterSvr, "-center=<ip>:<port> 指定中央服务器的连接地址")
	flag.StringVar(&tunKey, "tunnel", "000000", "-tunnel=<tunnelName> 指定tunnel的标识名")
	flag.Parse()
	tunnelKey = tunKey
	if tunnelKey == "000000" {
		tunnelKey = getInputTunnelKey()
	}

	getInputRemoteServer()
	fmt.Printf("正在连接到中央服务器[%s]\n", centersrv)

	connectCenter(centersrv)

	defer func() {
		if err := recover(); err != nil {
			fmt.Println(err) // 这里的err其实就是panic传入的内容，55
			time.Sleep(time.Second * 10)
		}
	}()
}
