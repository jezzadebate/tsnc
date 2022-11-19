package main

import (
	"context"
	"flag"
	"io"
	"log"
	"net"
	"os"
	"time"

	"github.com/mdp/qrterminal/v3"

	"tailscale.com/tsnet"
	"tailscale.com/types/logger"
)

var Config struct {
	hostname string
	host  string
	port  string
	noisy bool
	qr bool
}

// Stolem from: https://github.com/vfedoroff/go-netcat/blob/87e3e79d77ee6a0b236a784be83759a4d002a20d/main.go#L16
// Handles TC connection and perform synchorinization:
// TCP -> Stdout and Stdin -> TCP
func tcp_con_handle(con net.Conn) {
	chan_to_stdout := stream_copy(con, os.Stdout)
	chan_to_remote := stream_copy(os.Stdin, con)
	select {
	case <-chan_to_stdout:
		log.Println("Remote connection is closed")
	case <-chan_to_remote:
		log.Println("Local program is terminated")
	}
}

// Stolen from: https://github.com/vfedoroff/go-netcat/blob/87e3e79d77ee6a0b236a784be83759a4d002a20d/main.go#L28
// Performs copy operation between streams: os and tcp streams
func stream_copy(src io.Reader, dst io.Writer) <-chan int {
	buf := make([]byte, 1024)
	sync_channel := make(chan int)
	go func() {
		defer func() {
			if con, ok := dst.(net.Conn); ok {
				con.Close()
				log.Printf("Connection from %v is closed\n", con.RemoteAddr())
			}
			sync_channel <- 0 // Notify that processing is finished
		}()
		for {
			var nBytes int
			var err error
			nBytes, err = src.Read(buf)
			if err != nil {
				if err != io.EOF {
					log.Printf("Read error: %s\n", err)
				}
				break
			}
			_, err = dst.Write(buf[0:nBytes])
			if err != nil {
				log.Fatalf("Write error: %s\n", err)
			}
		}
	}()
	return sync_channel
}

func newTsNetServer() *tsnet.Server {
	var loggerF logger.Logf

	if !Config.noisy {
		loggerF = logger.Discard
	}

	return &tsnet.Server{
		Hostname:  Config.hostname,
		Ephemeral: true,
		Logf: loggerF,
	}
}

func dialAndCat(s *tsnet.Server) {
	ctx := context.Background()

	hostAndPort := Config.host + ":" + Config.port

	ts, err := s.Dial(ctx, "tcp", hostAndPort)
	if err != nil {
		log.Fatalln(err)
	}

	tcp_con_handle(ts)
}

func main() {
	flag.StringVar(&Config.host, "host", "bostonpi", "Host")
	flag.StringVar(&Config.port, "port", "22", "Port")
	flag.BoolVar(&Config.qr, "qr", false, "QR Code URLs")
	flag.BoolVar(&Config.noisy, "verbose", false, "Verbose Logging")
	flag.Parse()

	hostname := os.Getenv("TS_HOSTNAME")
	if hostname == "" {
		hostname = "tailscale-netcat"
	}

	Config.hostname = hostname

	if Config.host == "" || Config.port == "" {
		flag.PrintDefaults()
		os.Exit(1)
	}

	// Apparently this envvar needs to be set for this to work!
	err := os.Setenv("TAILSCALE_USE_WIP_CODE", "true")
	if err != nil {
		panic(err)
	}

	s := newTsNetServer()

	// s.Dial should do this but it wasnt working?
	s.Start()

	lc, _ := s.LocalClient()

	// Either this is doing something or it's enough of a race condition for Tailscale to start
	// before the Dial times out
	for i := 0; i < 60; i++ {
		st, err := lc.Status(context.Background())
		if err != nil {
			log.Fatal(err)
		}

		if st.BackendState == "NeedsLogin" {
			if st.AuthURL == "" {
				continue
			}
			if Config.qr {
				qrterminal.Generate(st.AuthURL, qrterminal.M, os.Stdout)
			}
			log.Printf("NeedsLogin: %s\n", st.AuthURL)
			for {
				time.Sleep(time.Second * 10)
				if st.BackendState == "Running" {
					break
				}
			}
		}

		if st.BackendState == "Running" {
			break
		}
		time.Sleep(time.Second)
	}

	dialAndCat(s)
}