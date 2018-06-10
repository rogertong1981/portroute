package main

import (
	"flag"
	"fmt"
	"net"
	"time"

	"portroute/common"
	"log"
	"os"
	"io"
)

type instance struct {
	key        string
	remoteSrv  string
	remoteConn net.Conn
	centerConn net.Conn
	exitChan   chan bool
}

var tunnelKey string
var instances = make(map[string]*instance)
var logger *log.Logger

func sendFwNotify(tunConn net.Conn, notifyMsg string) {
	common.WriteByte(tunConn, common.FwNotifyMessage)
	common.WriteString(tunConn, tunnelKey)
	common.WriteString(tunConn, notifyMsg)
}

func createInstance(centerSrv string, tunConn net.Conn, ins *instance) {
	remoteConn, err := net.Dial("tcp", ins.remoteSrv)
	if err != nil {
		notifyMsg := fmt.Sprintf("Instance[%v][%v]连接到后端服务器[%v]出错:\n%v\n", ins.key, ins.remoteSrv, ins.remoteSrv, err)
		logger.Print(notifyMsg)
		sendFwNotify(tunConn, notifyMsg)
		return
	}
	logger.Printf("正在建立Instance[%v][%v]中转连接\n", ins.key, ins.remoteSrv)
	ins.remoteConn = remoteConn
	defer func() {
		common.PrintError()
		remoteConn.Close()
		delete(instances, ins.key)
	}()

	centerConn, err1 := net.Dial("tcp", centerSrv)
	if err != nil {
		logger.Printf("Forward服务连接到中央服务器出错:\n%s\n", err1)
		return
	}

	defer func() {
		common.PrintError()
		centerConn.Close()
		delete(instances, ins.key)
		logger.Printf("Instance[%v][%v]连接关闭:\n", ins.key, ins.remoteSrv)
	}()

	ins.centerConn = centerConn
	common.WriteByte(centerConn, common.ForwardInstanceConn)
	common.WriteString(centerConn, tunnelKey)
	common.WriteString(centerConn, ins.key)
	logger.Printf("Instance[%v][%v]连接已就绪:\n", ins.key, ins.remoteSrv)

	go common.IoCopy(centerConn, remoteConn, ins.exitChan)
	go common.IoCopy(remoteConn, centerConn, ins.exitChan)
	<-ins.exitChan
}

func connectCenter(centerSrv string, tunKey string) {
	for k := range instances {
		delete(instances, k)
	}
	conn, err := net.Dial("tcp", centerSrv)
	if err != nil {
		time.Sleep(time.Second * 3)
		connectCenter(centerSrv, tunKey)
	}
	common.WriteByte(conn, common.ForwareTunnelConn)
	common.WriteString(conn, tunKey)
	go common.Ping(conn)
	if tunKey != "000000" {
		tunnelKey = tunKey
	}
	for {
		cmd, err := common.ReadByte(conn)
		if err != nil {
			logger.Printf("检测到中央服务器[%s]的连接已经断开，正在尝试重连...\n", centerSrv)
			connectCenter(centerSrv, tunnelKey)
		}
		switch cmd {
		case common.NotifyMessage:
			msg, _ := common.ReadString(conn)
			logger.Println(msg)
		case common.SetTunnelKey:
			key, _ := common.ReadString(conn)
			tunnelKey = key
			logger.Printf("已取得Tunnel的连接Key: %s\n", tunnelKey)
		case common.KickForwardTunnelConn:
			logger.Printf("有新的用户使用信道标示[%s]连接到中央服务器，你已经被踢下线!\n", tunnelKey)
			logger.Println("程序将在5秒后自动关闭!")
			time.Sleep(time.Second * 5)
			return
		case common.AddForwardLink:
			insKey, _ := common.ReadString(conn)
			remoteSrv, _ := common.ReadString(conn)
			delete(instances, insKey)
			ins := &instance{}
			ins.key = insKey
			ins.remoteSrv = remoteSrv
			ins.exitChan = make(chan bool, 1)
			instances[insKey] = ins
			go createInstance(centerSrv, conn, ins)
		}
	}
}

func main() {
	logFile, _ := os.OpenFile("forward.log", os.O_CREATE|os.O_APPEND|os.O_RDWR, os.ModePerm|os.ModeTemporary)
	writer:=io.MultiWriter(logFile,os.Stdout)
	logger = log.New(writer, "", log.LstdFlags)

	var centersrv string
	var tunKey string
	logger.Println("copyright by rogertong(tongbin@lonntec.com)")
	flag.StringVar(&centersrv, "center", common.DefaultCenterSvr, "-center=<ip>:<port> 指定中央服务器的连接地址")
	flag.StringVar(&tunKey, "tunnel", "000000", "-tunnel=<tunnelName> 指定tunnel的标识名")
	flag.Parse()
	logger.Printf("正在连接到中央服务器[%s]\n", centersrv)
	connectCenter(centersrv, tunKey)
}
