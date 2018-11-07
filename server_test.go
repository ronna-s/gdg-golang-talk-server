package main

import (
	"testing"
	"context"
	"net"
	"bufio"
)

const addr = ":9090"
const message = "sup?"

func TestRun(t *testing.T) {
	//iterating ensures we are releasing all the resources we are using
	//to prevent other tests from failing
	//and increases the chance to find race conditions
	for i := 0; i < 5; i++ {
		ready := make(chan struct{})
		ctx, cancel := context.WithCancel(context.Background())

		finished := make(chan struct{})

		go func() {
			Run(addr, ready, ctx)
			close(finished)
		}()

		<-ready //try removing this line
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			//cleanup resources to not affect other tests
			cancel()
			<-finished
			t.Fatal(err)
		}

		//write a message
		if _, err := conn.Write([]byte(message + "\n")); err != nil {
			t.Error(err)
		}

		//get your message back
		s, err := bufio.NewReader(conn).ReadString('\n')
		if err != nil {
			t.Error(err)
		}

		if s != message+"\n" {
			t.Errorf("Expected '%s' but received '%s'", message, s)
		}

		conn.Close()
		cancel()
		<-finished
	}
}

func TestServe(t *testing.T) {
	//iterating ensures we are releasing all the resources we are using
	//and increases the chance to find race conditions
	for i := 0; i < 5; i++ {
		l, err := net.Listen("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())

		finished := make(chan struct{})
		ready := make(chan struct{})

		handler := func(conn net.Conn, ctx context.Context) {
			close(ready)
			conn.Write([]byte(message + "\n"))
			<-ctx.Done() //block until cancel() to ensure it is called within the test
		}

		go func() {
			Serve(l, ctx, handler)
			close(finished)
		}()

		conn, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}

		<-ready //try removing this line
		//ready is closed when the connection handler function is invoked
		//it is then safe to close the listener
		l.Close()

		s, err := bufio.NewReader(conn).ReadString('\n')
		if s != message+"\n" {
			t.Fatalf("Expected '%s' but received '%s'", message, s)
		}

		cancel()
		conn.Close()
		//finished signals that all resources were released
		//it is safe it run as many iterations as we like
		<-finished
	}
}

func TestPersistAndEcho(t *testing.T) {
	//for i:=0; i<100; i++ {
	//	func() { // test is full of defers
	l, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	cliConn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal()
	}
	defer cliConn.Close()

	servConn, err := l.Accept()
	if err != nil {
		t.Fatal()
	}
	defer servConn.Close()

	mCh := make(chan []byte)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan struct{})

	go func() {
		PersistAndEcho(mCh, servConn, ctx)
		close(mCh)
		close(finished)
	}()

	cliConn.Write([]byte(message + "\n"))
	m := <-mCh //check message was persisted to the message channel mCh
	if string(m) != message {
		t.Fatalf("Expected '%s' but received '%s'", message, string(m))
	}

	s, err := bufio.NewReader(cliConn).ReadString('\n')
	if s != message+"\n" {
		t.Fatalf("Expected '%s' but received '%s'", message, s)
	}

	cliConn.Write([]byte("message with no delimiter"))

	cancel()
	<-mCh
	<-finished
	//	}()
	//}
}
