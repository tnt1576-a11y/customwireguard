package transport

import (
	"context"
	"net"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestDialEndpointPinnedLoopback(t *testing.T) {
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	index := 0
	for _, iface := range interfaces {
		if iface.Flags&(net.FlagLoopback|net.FlagUp) == net.FlagLoopback|net.FlagUp {
			index = iface.Index
			break
		}
	}
	if index == 0 {
		t.Fatal("no active loopback interface")
	}
	server, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client, err := DialEndpoint(context.Background(), server.LocalAddr().String(), index)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	raw, err := client.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var selected int
	var socketErr error
	if err := raw.Control(func(fd uintptr) {
		selected, socketErr = windows.GetsockoptInt(windows.Handle(fd), windows.IPPROTO_IP, ipUnicastIF)
	}); err != nil {
		t.Fatal(err)
	}
	if socketErr != nil || selected != index {
		t.Fatalf("binding did not persist after connect: index=%d err=%v", selected, socketErr)
	}
	server.SetDeadline(time.Now().Add(2 * time.Second))
	client.SetDeadline(time.Now().Add(2 * time.Second))
	for n := 0; n < 8; n++ {
		payload := []byte{byte(n), 19, 42}
		if _, err := client.Write(payload); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 32)
		size, peer, err := server.ReadFromUDP(buf)
		if err != nil || string(buf[:size]) != string(payload) {
			t.Fatalf("server receive: %x %v", buf[:size], err)
		}
		if _, err := server.WriteToUDP(buf[:size], peer); err != nil {
			t.Fatal(err)
		}
		size, err = client.Read(buf)
		if err != nil || string(buf[:size]) != string(payload) {
			t.Fatalf("client receive: %x %v", buf[:size], err)
		}
	}
}

func TestDialEndpointBindingFailsClosed(t *testing.T) {
	for _, index := range []int{-1, 0x1000000, 0xffffff} {
		if index == 0xffffff {
			if _, err := net.InterfaceByIndex(index); err == nil {
				t.Fatal("test requires an unused interface index")
			}
		}
		conn, err := DialEndpoint(context.Background(), "127.0.0.1:9", index)
		if conn != nil || err == nil {
			if conn != nil {
				conn.Close()
			}
			t.Fatalf("invalid interface %d fell back: %v", index, err)
		}
	}
	if conn, err := DialEndpoint(context.Background(), "[::1]:9", 1); conn != nil || err == nil {
		if conn != nil {
			conn.Close()
		}
		t.Fatal("accepted unsupported IPv6 binding")
	}
}

func TestListenEndpointPinnedForQUIC(t *testing.T) {
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	index := 0
	for _, iface := range interfaces {
		if iface.Flags&(net.FlagLoopback|net.FlagUp) == net.FlagLoopback|net.FlagUp {
			index = iface.Index
			break
		}
	}
	if index == 0 {
		t.Fatal("no loopback interface")
	}
	server := socket(t)
	client, err := ListenEndpoint(context.Background(), server.LocalAddr().String(), index)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	raw, err := client.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var selected int
	var socketErr error
	err = raw.Control(func(fd uintptr) {
		selected, socketErr = windows.GetsockoptInt(windows.Handle(fd), windows.IPPROTO_IP, ipUnicastIF)
	})
	if err != nil || socketErr != nil || selected != index {
		t.Fatalf("unconnected socket binding failed: %d %v %v", selected, err, socketErr)
	}
	server.SetDeadline(time.Now().Add(time.Second))
	client.SetDeadline(time.Now().Add(time.Second))
	if _, err = client.WriteToUDP([]byte("quic socket"), server.LocalAddr().(*net.UDPAddr)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	n, peer, err := server.ReadFromUDP(buf)
	if err != nil {
		t.Fatal(err)
	}
	server.WriteToUDP(buf[:n], peer)
	n, _, err = client.ReadFromUDP(buf)
	if err != nil || string(buf[:n]) != "quic socket" {
		t.Fatalf("unconnected receive: %v", err)
	}
}
