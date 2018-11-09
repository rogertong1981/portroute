package main

import (
	"flag"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
	"portroute/common"
	"log"
	"os"
	"io"
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
}

var tunnelKey string
var centerSrv string
type arrayFlags []string
var remoteAddrs arrayFlags

var linkInfos = make(map[string]*linkInfo)
var listenInfos = make(map[string]*listenInfo)
var logger *log.Logger
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

func AppendLinkInfo(serverAddr string,serverPort int,listenPort int)  {
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
}

func getInputRemoteServer() {
	fmt.Println("请输入你需要添加的后端服务器信息")
	var serverAddr string
	var appendNext string
	fmt.Print("服务器地址:")
	fmt.Scanln(&serverAddr)
	serverPort := getInputPort("服务器端口号:")
	listenPort := getInputPort("本地监听端口号:")
	AppendLinkInfo(serverAddr, serverPort, listenPort)
	//if info, ok := linkInfos[key]; !ok {
	//	var m sync.Mutex
	//	m.Lock()
	//	defer m.Unlock()
	//	info = &linkInfo{}
	//	info.key = key
	//	info.listenPort = listenPort
	//	info.serverAddr = serverAddr
	//	info.serverPort = serverPort
	//	linkInfos[key] = info
	//}

	fmt.Print("是否还需要添加后端服务器？(y/n)")
	fmt.Scanln(&appendNext)
	if appendNext == "y" || appendNext == "Y" {
		getInputRemoteServer()
	}
}

func getRemoteServerFromFlag() {
	for _, v := range remoteAddrs {
		strs := strings.Split(v, ":")
		if len(strs) != 3 {
			continue
		}
		serverAddr := strs[0]
		serverPortStr := strs[1]
		listenPortStr := strs[2]

		serverPort, e1 := strconv.Atoi(serverPortStr)
		listenPort, e2 := strconv.Atoi(listenPortStr)
		if e1 != nil || e2 != nil || (serverPort < 1 || serverPort > 65535) || (listenPort < 1 || listenPort > 65535) {
			logger.Printf("指定的远程服务器无效：%s", v)
			continue
		}
		AppendLinkInfo(serverAddr, serverPort, listenPort)
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
	ins := &instance{}
	ins.key = getNewInstanceKey(link)
	ins.exitChan = make(chan bool, 1)
	ins.proxyConn = conn
	remoteSrv := fmt.Sprintf("%v:%v", link.linkInfo.serverAddr, link.linkInfo.serverPort)
	logger.Printf("正在建立Proxy-Instance[%v][%v]转发连接...\n", ins.key, remoteSrv)
	fwConn, err := net.Dial("tcp", centerSrv)
	if err != nil {
		logger.Printf("建立Proxy-Instance[%v][%v]转发连接时出错:\n%v\n", ins.key, remoteSrv, err)
		return
	}
	defer func() {
		fwConn.Close()
		conn.Close()
		logger.Printf("Forward-Instance[%v][%v]连接已关闭:\n", ins.key, remoteSrv)
	}()
	ins.forwardConn = fwConn
	common.WriteByte(fwConn, common.ProxyInstanceConn)
	common.WriteString(fwConn, tunnelKey)
	common.WriteString(fwConn, ins.key)

	common.WriteString(fwConn, remoteSrv)
	cmd, _ := common.ReadByte(fwConn)
	if cmd == common.ForwardLinkSuccess {
		logger.Printf("Forward-Instance[%v][%v]连接已就绪:\n", ins.key, remoteSrv)
		go common.IoCopy(ins.forwardConn, ins.proxyConn, ins.exitChan)
		go common.IoCopy(ins.proxyConn, ins.forwardConn, ins.exitChan)
		<-ins.exitChan
	}
}

func createListen(info *listenInfo) {
	lis, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%v", info.linkInfo.listenPort))
	if err != nil {
		logger.Printf("打开本地监听端口[%v]失败：\n%v\n", info.linkInfo.listenPort, err)
		return
	}
	defer lis.Close()

	for {
		conn, err := lis.Accept()
		if err != nil {
			logger.Println(err)
			continue
		}
		go acceptProxyInstanceConnect(conn, info)
	}
}

func createListens() {
	for _, v := range linkInfos {
		info := getListenInfo(v)
		go createListen(info)
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

	common.WriteByte(conn, common.ProxyTunnelConn)
	common.WriteString(conn, tunnelKey)
	go common.Ping(conn)
	for {
		cmd, err := common.ReadByte(conn)
		if err != nil {
			logger.Printf("检测到中央服务器[%s]的连接已经断开，正在尝试重连...\n", centerSrv)
			connectCenter(centerSrv)
		}
		switch cmd {
		case common.NotifyMessage:
			msg, _ := common.ReadString(conn)
			logger.Println(msg)
		case common.KickProxyTunnelConn:
			logger.Printf("有新的用户使用信道标示[%s]连接到中央服务器，你已经被踢下线!\n", tunnelKey)
			logger.Println("程序将在5秒后自动关闭!")
			time.Sleep(time.Second * 5)
			return
		}
	}
}

func (i *arrayFlags) String() string {
	return fmt.Sprint(*i)
}

// Set 方法是flag.Value接口, 设置flag Value的方法.
// 通过多个flag指定的值， 所以我们追加到最终的数组上.
func (i *arrayFlags) Set(value string) error {
	*i = append(*i, value)
	return nil
}

func main() {
	logFile, _ := os.OpenFile("proxy.log", os.O_CREATE|os.O_APPEND|os.O_RDWR, os.ModePerm|os.ModeTemporary)
	writer:=io.MultiWriter(logFile,os.Stdout)
	logger = log.New(writer, "", log.LstdFlags)
	var tunKey string
	logger.Println("copyright by rogertong(tongbin@lonntec.com)")
	flag.StringVar(&centerSrv, "center", common.DefaultCenterSvr, "-center=<ip>:<port> 指定中央服务器的连接地址")
	flag.StringVar(&tunKey, "tunnel", "000000", "-tunnel=<tunnelName> 指定tunnel的标识名")
	flag.Var(&remoteAddrs,"remotes","-remotes=<ipaddr:port ipaddr:port> 指定需要连接的远程服务器地址,本参数可以重复使用")
	flag.Parse()
	tunnelKey = tunKey
	if tunnelKey == "000000" {
		tunnelKey = getInputTunnelKey()
	}

	getRemoteServerFromFlag()
	if len(linkInfos)<=0 {
		getInputRemoteServer()
	}
	logger.Printf("正在连接到中央服务器[%s]\n", centerSrv)

	connectCenter(centerSrv)

	defer common.PrintError()
}
