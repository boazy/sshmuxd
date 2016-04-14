package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"os"

	"github.com/boazy/sshmux"

	"golang.org/x/crypto/ssh"
)

func usage() {
	fmt.Printf("Usage: \n")
	fmt.Printf("   %s conf\n", os.Args[0])
}

type Host struct {
	Address string   `json:"address"`
	Users   []string `json:"users"`
	NoAuth  bool     `json:"noAuth"`
}

type Conf struct {
	Address  string `json:"address"`
	HostKey  string `json:"hostkey"`
	AuthKeys string `json:"authkeys"`
	Hosts    []Host `json:"hosts"`
}

func parseConf(filename string) (*Conf, error) {
	f, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	c := &Conf{}
	err = json.Unmarshal(f, c)
	if err != nil {
		return nil, err
	}

	return c, nil
}

func parseAuthFile(filename string) ([]*sshmux.User, error) {
	var users []*sshmux.User

	authFile, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	// Parse authfile as authorized_key

	for len(authFile) > 0 {
		var (
			pk      ssh.PublicKey
			comment string
		)

		pk, comment, _, authFile, err = ssh.ParseAuthorizedKey(authFile)
		if err != nil {
			return nil, err
		}

		u := &sshmux.User{
			PublicKey: pk,
			Name:      comment,
		}

		users = append(users, u)
	}

	return users, nil
}

func main() {
	// Config
	if len(os.Args) != 2 {
		usage()
		return
	}

	conf := os.Args[1]

	c, err := parseConf(conf)
	if err != nil {
		panic(err)
	}

	hostPrivateKey, err := ioutil.ReadFile(c.HostKey)
	if err != nil {
		panic(err)
	}

	hostSigner, err := ssh.ParsePrivateKey(hostPrivateKey)
	if err != nil {
		panic(err)
	}

	users, err := parseAuthFile(c.AuthKeys)
	if err != nil {
		panic(err)
	}

	hasDefaults := false
	for _, h := range c.Hosts {
		if h.NoAuth {
			hasDefaults = true
			break
		}
	}

	// sshmux setup
	auth := func(c ssh.ConnMetadata, key ssh.PublicKey) (*sshmux.User, error) {
		t := key.Type()
		k := key.Marshal()
		for i := range users {
			candidate := users[i].PublicKey
			if t == candidate.Type() && bytes.Compare(k, candidate.Marshal()) == 0 {
				return users[i], nil
			}
		}

		if hasDefaults {
			return nil, nil
		}

		log.Printf("%s: access denied (username: %s)", c.RemoteAddr(), c.User())
		return nil, errors.New("access denied")
	}

	setup := func(session *sshmux.Session) error {
		var username string
		if session.User != nil {
			username = session.User.Name
		} else {
			username = "unknown user"
		}
		log.Printf("%s: %s authorized (username: %s)", session.Conn.RemoteAddr(), username, session.Conn.User())

	outer:
		for _, h := range c.Hosts {
			if h.NoAuth {
				session.Remotes = append(session.Remotes, h.Address)
				continue outer
			}

			if session.User == nil {
				continue
			}

			for _, u := range h.Users {
				if u == session.User.Name {
					session.Remotes = append(session.Remotes, h.Address)
					continue outer
				}
			}
		}
		return nil
	}

	server := sshmux.New(hostSigner, auth, setup)
	server.Selected = func(session *sshmux.Session, remote string) error {
		var username string
		if session.User != nil {
			username = session.User.Name
		} else {
			username = "unknown user"
		}
		log.Printf("%s: %s connecting to %s", session.Conn.RemoteAddr(), username, remote)
		return nil
	}

	// Set up listener
	l, err := net.Listen("tcp", c.Address)
	if err != nil {
		panic(err)
	}

	server.Serve(l)
}
