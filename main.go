// Copyright 2013 Joe Walnes and the websocketd team.
// All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"io"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"path/filepath"
	"os"
	"errors"

	"code.google.com/p/go.net/websocket"
)

func main() {
	flag.Usage = PrintHelp
	config := parseCommandLine()

	http.Handle(config.BasePath, websocket.Handler(func(ws *websocket.Conn) {
		acceptWebSocket(ws, &config)
	}))

	if config.Verbose {
		if config.UsingScriptDir {
			log.Print("Listening on ws://", config.Addr, config.BasePath, " -> ", config.ScriptDir)
		} else {
			log.Print("Listening on ws://", config.Addr, config.BasePath, " -> ", config.CommandName, " ", strings.Join(config.CommandArgs, " "))
		}
	}
	err := http.ListenAndServe(config.Addr, nil)
	if err != nil {
		log.Fatal(err)
	}
}

var ScriptNotFoundError = errors.New("script not found")

type URLInfo struct {
	ScriptPath string
	PathInfo   string
	FilePath   string
}

func parsePath(path string, config *Config) (*URLInfo, error) {
	if !config.UsingScriptDir {
		return &URLInfo{"/", path, ""}, nil
	}
	
	parts := strings.Split(path[1:], "/")
	urlInfo := &URLInfo{}

	for i, part := range parts {
		urlInfo.ScriptPath = strings.Join([]string{urlInfo.ScriptPath, part}, "/")
		urlInfo.FilePath = filepath.Join(config.ScriptDir, urlInfo.ScriptPath)
		isLastPart := i == len(parts) - 1
		statInfo, err := os.Stat(urlInfo.FilePath)

		// not a valid path
		if err != nil {
			return nil, ScriptNotFoundError
		}

		// at the end of url but is a dir
		if isLastPart && statInfo.IsDir() {
			return nil, ScriptNotFoundError
		}

		// we've hit a dir, carry on looking
		if statInfo.IsDir() {
			continue
		} 

		// no extra args
		if isLastPart {
			return urlInfo, nil
		}

		// build path info from extra parts of url
		urlInfo.PathInfo = "/" + strings.Join(parts[i+1:], "/")
		return urlInfo, nil
	}
	panic("parsePath")
}

func acceptWebSocket(ws *websocket.Conn, config *Config) {
	defer ws.Close()

	if config.Verbose {
		log.Print("websocket: CONNECT")
		defer log.Print("websocket: DISCONNECT")
	}

	urlInfo, err := parsePath(ws.Request().URL.Path, config)
	if err != nil {
		// TODO: 404?
		log.Print(err)
		return
	}

	if config.Verbose {
		log.Print("process: URLInfo - ", urlInfo)
	}

	env, err := createEnv(ws, config, urlInfo)
	if err != nil {
		if config.Verbose {
			log.Print("process: Could not setup env: ", err)
		}
		return
	}
	
	commandName := config.CommandName
	if config.UsingScriptDir {
		commandName = urlInfo.FilePath
	}

	_, stdin, stdout, err := execCmd(commandName, config.CommandArgs, env)
	if err != nil {
		if config.Verbose {
			log.Print("process: Failed to start: ", err)
		}
		return
	}

	done := make(chan bool)

	outbound := make(chan string)
	go readProcess(stdout, outbound, done, config)
	go writeWebSocket(ws, outbound, done, config)

	inbound := make(chan string)
	go readWebSocket(ws, inbound, done, config)
	go writeProcess(stdin, inbound, done, config)

	<-done
}

func execCmd(commandName string, commandArgs []string, env []string) (*exec.Cmd, io.WriteCloser, io.ReadCloser, error) {
	cmd := exec.Command(commandName, commandArgs...)
	cmd.Env = env

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return cmd, nil, nil, err
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return cmd, nil, nil, err
	}

	err = cmd.Start()
	if err != nil {
		return cmd, nil, nil, err
	}

	return cmd, stdin, stdout, err
}