package main

import (
	"log"
	"net"
	"sync"
	"os"
	"os/signal"
	"context"
	"runtime"
	"fmt"
	"syscall"
	"bufio"
	"time"
)

var aLongTimeAgo = time.Unix(233431200, 0)

//Our super important operation that must not be interrupted in the middle
func PersistAndEcho(mCh chan []byte, conn net.Conn, ctx context.Context) {
	go func() {
		<-ctx.Done()
		// Found a nice cheat!
		// According to docs - SetReadDeadline sets the deadline
		// for future Read calls
		// ***and any currently-blocked Read call***
		// Yay!
		conn.SetReadDeadline(aLongTimeAgo)
		log.Println("Connection context cancelled.")
	}()

	s:=bufio.NewScanner(conn)
	for s.Scan(){
		mCh <- s.Bytes()
		conn.Write(s.Bytes())
		conn.Write([]byte("\n"))
	}
	log.Println("Closing connection")
	conn.Close()
}

type Handler func(conn net.Conn, ctx context.Context)

func Serve(l net.Listener, ctx context.Context, handle Handler) (err error) {
	var wg sync.WaitGroup
	var conn net.Conn
	for {
		conn, err = l.Accept()
		if err != nil {
			break
		}
		log.Println("Accepted connection")
		wg.Add(1)
		go func(conn net.Conn) {
			connCtx, cancel := context.WithCancel(ctx)
			defer func() {
				cancel()
				conn.Close() //design choice here
				wg.Done()
			}()
			handle(conn, connCtx)
		}(conn)
	}
	wg.Wait()
	return err
}

func Run(addr string, ready chan struct{}, ctx context.Context) {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		panic(err)
	}
	close(ready)      //signal that we are listening
	runtime.Gosched() //not necessary - ensures the "listening" log message is first

	var wg sync.WaitGroup
	wg.Add(2)

	//goroutine 1:
	//handle context cancellation
	//It starts the termination process by closing the listener
	//wg.Done is not necessary here, since it terminates the others
	go func() {
		<-ctx.Done()
		log.Println("Context cancelled. Terminating...")
		if err := l.Close(); err != nil {
			panic(err)
		}
	}()

	mCh := make(chan []byte)
	//goroutine 2:
	//Serve: Accepts connections and spawns goroutines to handle them
	//Serve exists when l.Accept fails (we trigger this behavior by closing
	//the listener in goroutine 1
	//Since it exists after all goroutines have finished writing to our shared
	//message channel mCh, we can close mCh, thereby causing the graceful termination
	//of goroutine 3
	go func() {
		defer func() {
			//Serve finishes only when all messages
			//have been persisted, we can safely close mCh
			log.Println("Serve finished. Terminating...")
			close(mCh)
			wg.Done()
		}()

		Serve(l, ctx, func(conn net.Conn, ctx context.Context) {
			//supposedly PersistAndEcho is a very important operation
			//that must not be terminated in the middle
			//it writes []byte message to mCh
			PersistAndEcho(mCh, conn, ctx)
		})
	}()

	//goroutine 3:
	//Iterate over mCh (the channels all the TCP handlers are writing to
	//It exists when mCh is closed (see: goroutine 2)
	go func() {
		defer func() {
			log.Println("Messages channel closed. Terminating...")
			wg.Done()
		}()
		for m := range mCh {
			fmt.Println("Received message:", string(m))
		}
	}()

	wg.Wait()
}

func main() {
	const addr = ":9090"

	//create a cancellable context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	//create a channel for singals, and register for signal interrupt.
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT)

	go func() {
		//upon receiving sigint, cancel the context
		//if everything is done properly, the program will terminate gracefully
		//if not, the prorgam will not exit and you will have to kill it.
		<-sigc
		log.Println("Received SIGINT... Canceling the context")
		cancel()
	}()

	done := make(chan struct{})
	ready := make(chan struct{})

	go func() {
		Run(addr, ready, ctx)
		close(done)
	}()

	<-ready //our signal from inside Run that the app is ready.
	log.Println("App is ready to accept connections")
	<-done //our signal that Run has finished and we can exit.
}
