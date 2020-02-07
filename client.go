package gremtune

import (
	"io/ioutil"
	"log"
	"sync"

	"github.com/pkg/errors"
)

// Client is a container for the gremtune client.
type Client struct {
	conn                   dialer
	requests               chan []byte
	responses              chan []byte
	results                *sync.Map
	responseNotifier       *sync.Map // responseNotifier notifies the requester that a response has arrived for the request
	responseStatusNotifier *sync.Map // responseStatusNotifier notifies the requester that a response has arrived for the request with the code
	sync.RWMutex
	Errored bool
}

func newClient() (c Client) {
	c.requests = make(chan []byte, 3)  // c.requests takes any request and delivers it to the WriteWorker for dispatch to Gremlin Server
	c.responses = make(chan []byte, 3) // c.responses takes raw responses from ReadWorker and delivers it for sorting to handelResponse
	c.results = &sync.Map{}
	c.responseNotifier = &sync.Map{}
	c.responseStatusNotifier = &sync.Map{}
	return
}

// Dial returns a gremtune client for interaction with the Gremlin Server specified in the host IP.
func Dial(conn dialer, errs chan error) (c Client, err error) {
	c = newClient()
	c.conn = conn

	// Connects to Gremlin Server
	err = conn.connect()
	if err != nil {
		return
	}

	quit := conn.(*websocket).quit

	go c.writeWorker(errs, quit)
	go c.readWorker(errs, quit)
	go conn.ping(errs)

	return
}

func (c *Client) executeRequest(query string, bindings, rebindings *map[string]string) (resp []Response, err error) {
	var req request
	var id string
	if bindings != nil && rebindings != nil {
		req, id, err = prepareRequestWithBindings(query, *bindings, *rebindings)
	} else {
		req, id, err = prepareRequest(query)
	}
	if err != nil {
		return
	}

	msg, err := packageRequest(req)
	if err != nil {
		log.Println(err)
		return
	}
	c.responseNotifier.Store(id, make(chan error, 1))
	c.responseStatusNotifier.Store(id, make(chan int, 1))
	c.dispatchRequest(msg)
	resp, err = c.retrieveResponse(id)
	if err != nil {
		err = errors.Wrapf(err, "query: %s", query)
	}
	return
}

func (c *Client) executeAsync(query string, bindings, rebindings *map[string]string, responseChannel chan AsyncResponse) (err error) {
	var req request
	var id string
	if bindings != nil && rebindings != nil {
		req, id, err = prepareRequestWithBindings(query, *bindings, *rebindings)
	} else {
		req, id, err = prepareRequest(query)
	}
	if err != nil {
		return
	}

	msg, err := packageRequest(req)
	if err != nil {
		log.Println(err)
		return
	}
	c.responseNotifier.Store(id, make(chan error, 1))
	c.responseStatusNotifier.Store(id, make(chan int, 1))
	c.dispatchRequest(msg)
	go c.retrieveResponseAsync(id, responseChannel)
	return
}

func (c *Client) authenticate(requestID string) (err error) {
	auth := c.conn.getAuth()
	req, err := prepareAuthRequest(requestID, auth.username, auth.password)
	if err != nil {
		return
	}

	msg, err := packageRequest(req)
	if err != nil {
		log.Println(err)
		return
	}

	c.dispatchRequest(msg)
	return
}

// ExecuteWithBindings formats a raw Gremlin query, sends it to Gremlin Server, and returns the result.
func (c *Client) ExecuteWithBindings(query string, bindings, rebindings map[string]string) (resp []Response, err error) {
	if c.conn.IsDisposed() {
		return resp, errors.New("you cannot write on disposed connection")
	}
	resp, err = c.executeRequest(query, &bindings, &rebindings)
	return
}

// Execute formats a raw Gremlin query, sends it to Gremlin Server, and returns the result.
func (c *Client) Execute(query string) (resp []Response, err error) {
	if c.conn.IsDisposed() {
		return resp, errors.New("you cannot write on disposed connection")
	}
	resp, err = c.executeRequest(query, nil, nil)
	return
}

// Execute formats a raw Gremlin query, sends it to Gremlin Server, and the results are streamed to channel provided in method paramater.
func (c *Client) ExecuteAsync(query string, responseChannel chan AsyncResponse) (err error) {
	if c.conn.IsDisposed() {
		return errors.New("you cannot write on disposed connection")
	}
	err = c.executeAsync(query, nil, nil, responseChannel)
	return
}

// ExecuteFileWithBindings takes a file path to a Gremlin script, sends it to Gremlin Server with bindings, and returns the result.
func (c *Client) ExecuteFileWithBindings(path string, bindings, rebindings map[string]string) (resp []Response, err error) {
	if c.conn.IsDisposed() {
		return resp, errors.New("you cannot write on disposed connection")
	}
	d, err := ioutil.ReadFile(path) // Read script from file
	if err != nil {
		log.Println(err)
		return
	}
	query := string(d)
	resp, err = c.executeRequest(query, &bindings, &rebindings)
	return
}

// ExecuteFile takes a file path to a Gremlin script, sends it to Gremlin Server, and returns the result.
func (c *Client) ExecuteFile(path string) (resp []Response, err error) {
	if c.conn.IsDisposed() {
		return resp, errors.New("you cannot write on disposed connection")
	}
	d, err := ioutil.ReadFile(path) // Read script from file
	if err != nil {
		log.Println(err)
		return
	}
	query := string(d)
	resp, err = c.executeRequest(query, nil, nil)
	return
}

// Close closes the underlying connection and marks the client as closed.
func (c *Client) Close() {
	if c.conn != nil {
		c.conn.close()
	}
}

func (c *Client) writeWorker(errs chan error, quit chan struct{}) { // writeWorker works on a loop and dispatches messages as soon as it receives them
	for {
		select {
		case msg := <-c.requests:
			c.Lock()
			err := c.conn.write(msg)
			if err != nil {
				errs <- err
				c.Errored = true
				c.Unlock()
				break
			}
			c.Unlock()

		case <-quit:
			return
		}
	}
}

func (c *Client) readWorker(errs chan error, quit chan struct{}) { // readWorker works on a loop and sorts messages as soon as it receives them
	for {
		msgType, msg, err := c.conn.read()
		if msgType == -1 { // msgType == -1 is noFrame (close connection)
			return
		}
		if err != nil {
			errs <- errors.Wrapf(err, "Receive message type: %d", msgType)
			c.Errored = true
			break
		}
		if msg != nil {
			// FIXME: At the moment the error returned by handle response is just ignored.
			err = c.handleResponse(msg)
			_ = err
		}

		select {
		case <-quit:
			return
		default:
			continue
		}
	}
}
