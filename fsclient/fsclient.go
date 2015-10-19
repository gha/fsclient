//Package fsclient provides a client for the Freeswitch Event Socket.
package fsclient

import (
	"errors"
	"io"
	"net"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"time"
)

//Client represents a Freeswitch client. Contains the event socket connection.
type Client struct {
	eventConn  *textproto.Conn
	eventQueue []map[string]string
}

//NewClient initialises a new Freeswitch client.
func NewClient() *Client {
	return &Client{
		eventQueue: make([]map[string]string, 0),
	}
}

//Connect establishes a connection with the local Freeswitch server.
func (client *Client) Connect() (err error) {
	//Connect to Freeswitch Event Socket.
	conn, err := net.DialTimeout("tcp", "127.0.0.1:8021",
		time.Duration(5*time.Second))
	if err != nil {
		return
	}

	//Convert the raw TCP connection to a textproto connection.
	client.eventConn = textproto.NewConn(conn)

	//Read the welcome message.
	resp, err := client.eventConn.ReadMIMEHeader()
	if err != nil {
		return
	}

	//Send authentication request to server.
	client.eventConn.PrintfLine("auth %s\r\n", "ClueCon")

	if resp, err = client.eventConn.ReadMIMEHeader(); err != nil {
		return
	}

	//Check the command was processed OK.
	if resp.Get("Content-Type") == "command/reply" &&
		resp.Get("Reply-Text") == "+OK accepted" {
		return
	}

	return errors.New("Could not authenticate")
}

//AddFilter specifies event types to listen for.
//Note, this is not a filter out but rather a "filter in," that is, when a
//filter is applied only the filtered values are received.
//Multiple filters on a socket connection are allowed.
func (client *Client) AddFilter(arg string) (err error) {
	//Send filter command to server.
	client.eventConn.PrintfLine("filter %s\r\n", arg)

	resp, err := client.eventConn.ReadMIMEHeader()
	if err != nil {
		return
	}

	//Check the command was processed OK.
	if resp.Get("Content-Type") == "command/reply" &&
		resp.Get("Reply-Text") == "+OK" {
		return
	}

	return errors.New("Could not add filter")
}

//SubcribeEvent enables events by class or all.
func (client *Client) SubcribeEvent(arg string) (err error) {
	//Send event command to server.
	client.eventConn.PrintfLine("event plain %s\r\n", arg)

	resp, err := client.eventConn.ReadMIMEHeader()
	if err != nil {
		return
	}

	//Check the command was processed OK.
	if resp.Get("Content-Type") == "command/reply" &&
		resp.Get("Reply-Text") == "+OK" {
		return
	}

	return errors.New("Could not subcribe to event")
}

//API sends an api command (blocking mode).
func (client *Client) API(cmd string) (string, error) {
	//Send API command to the server.
	client.eventConn.PrintfLine("api %s\r\n", cmd)
	event, err := client.readMsg(true)
	return event["body"], err
}

//Execute is used to execute dialplan applications on a channel.
func (client *Client) Execute(app string, arg string, uuid string, lock bool) (err error) {
	//Send execute command to server.
	client.eventConn.PrintfLine("sendmsg %s", uuid)
	client.eventConn.PrintfLine("call-command: execute")
	client.eventConn.PrintfLine("execute-app-name: %s", app)

	if arg != "" {
		client.eventConn.PrintfLine("execute-app-arg: %s", arg)
	}

	if lock {
		client.eventConn.PrintfLine("event-lock: true")
	}

	client.eventConn.PrintfLine("") //Empty line indicates end of command.
	_, err = client.readMsg(true)
	return err
}

//ReadEvent receives a single event from the Freeswitch socket (blocking mode).
func (client *Client) ReadEvent() (map[string]string, error) {
	return client.readMsg(false)
}

//ReadEvent receives a single message from the Freeswitch socket (blocking mode).
func (client *Client) readMsg(cmdResponse bool) (map[string]string, error) {
	//If unprocessed events in local queue, return them first.
	if !cmdResponse && len(client.eventQueue) > 0 {
		popped := client.eventQueue[0]
		client.eventQueue = client.eventQueue[1:]
		return popped, nil
	}

	//Read next message off Freeswitch connection.
MsgLoop:
	for {
		//Intialises a key/value pair map to put event into.
		event := make(map[string]string)
		resp, err := client.eventConn.ReadMIMEHeader()
		if err != nil {
			return nil, err
		}

		if resp.Get("Content-Type") == "text/event-plain" &&
			resp.Get("Content-Length") != "" {
			//Handle event message type.
			//Check that Content-Length is numeric.
			_, err := strconv.Atoi(resp.Get("Content-Length"))
			if err != nil {
				return nil, err
			}

			for {
				//Read each line of the event and store into map.
				line, err := client.eventConn.ReadLine()
				if err != nil {
					return event, err
				}

				if line == "" { //Empty line means end of event.
					if cmdResponse {
						client.eventQueue = append(client.eventQueue, event)
						continue MsgLoop
					}
					return event, nil
				}

				parts := strings.Split(line, ": ") //Split "Key: value"
				key := parts[0]
				value, err := url.QueryUnescape(parts[1])

				if err != nil {
					return event, err
				}

				event[key] = value
			}
			return event, nil
		} else if resp.Get("Content-Type") == "api/response" &&
			resp.Get("Content-Length") != "" {
			//Handle "api" response message.
			//Check that Content-Length is numeric.
			length, err := strconv.Atoi(resp.Get("Content-Length"))
			if err != nil {
				return event, err
			}

			//Read Content-Length bytes into a buffer and convert to string.
			buf := make([]byte, length)
			if _, err = io.ReadFull(client.eventConn.R, buf); err != nil {
				return event, err
			}
			event["body"] = string(buf)
			return event, nil
		} else if resp.Get("Content-Type") == "command/reply" &&
			resp.Get("Reply-Text") == "+OK" {
			//Handle "execute" response message.
			event["body"] = "OK"
			return event, err
		}

		return nil, errors.New("Unexpected read error")
	}
}
