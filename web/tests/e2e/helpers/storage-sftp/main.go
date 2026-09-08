// Test-only SFTP endpoint. It listens on loopback, uses a per-run password and
// an in-memory filesystem, and provides no shell or access to the host's files.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"fmt"
	"log"
	"net"
	"os"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

func main() {
	password := os.Getenv("RENART_SFTP_FIXTURE_PASSWORD")
	if len(password) < 20 {
		log.Fatal("a per-run fixture password is required")
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	check(err)
	signer, err := ssh.NewSignerFromKey(key)
	check(err)
	config := &ssh.ServerConfig{PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
		if c.User() == "fixture" && subtle.ConstantTimeCompare(pass, []byte(password)) == 1 {
			return nil, nil
		}
		return nil, fmt.Errorf("invalid fixture credentials")
	}}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	check(err)
	defer listener.Close()
	files := sftp.InMemHandler()
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go serve(connection, config, files)
		}
	}()
	client, err := ssh.Dial("tcp", listener.Addr().String(), &ssh.ClientConfig{
		User: "fixture", Auth: []ssh.AuthMethod{ssh.Password(password)},
		HostKeyCallback: ssh.FixedHostKey(signer.PublicKey()), Timeout: 5 * time.Second,
	})
	check(err)
	storage, err := sftp.NewClient(client)
	check(err)
	check(storage.Mkdir("/incoming"))
	check(storage.Mkdir("/outgoing"))
	file, err := storage.Create("/incoming/orders.csv")
	check(err)
	_, err = file.Write([]byte("id,amount\n1,10\n2,20\n"))
	check(err)
	check(file.Close())
	check(storage.Close())
	check(client.Close())
	fmt.Println(listener.Addr().(*net.TCPAddr).Port)
	select {}
}

func serve(connection net.Conn, config *ssh.ServerConfig, files sftp.Handlers) {
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(10 * time.Second))
	server, channels, requests, err := ssh.NewServerConn(connection, config)
	if err != nil {
		return
	}
	defer server.Close()
	_ = connection.SetDeadline(time.Now().Add(2 * time.Minute))
	go ssh.DiscardRequests(requests)
	for incoming := range channels {
		if incoming.ChannelType() != "session" {
			_ = incoming.Reject(ssh.UnknownChannelType, "SFTP only")
			continue
		}
		channel, requests, err := incoming.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer channel.Close()
			for request := range requests {
				var subsystem struct{ Name string }
				accepted := request.Type == "subsystem" && ssh.Unmarshal(request.Payload, &subsystem) == nil && subsystem.Name == "sftp"
				_ = request.Reply(accepted, nil)
				if accepted {
					go ssh.DiscardRequests(requests)
					fs := sftp.NewRequestServer(channel, files)
					_ = fs.Serve()
					_ = fs.Close()
					return
				}
			}
		}()
	}
}

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
