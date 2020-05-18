package openstack

import (
	"fmt"
	"github.com/gophercloud/gophercloud"
	"sync"
)

// use projectID as key
type ClientCache struct {
	mu             sync.RWMutex
	clientMap      map[string]*gophercloud.ServiceClient
	userCredential map[string]*UserCredential
}

func (c *ClientCache) getClient(id string) (*gophercloud.ServiceClient, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	cli, ok := c.clientMap[id]
	if !ok {
		err := fmt.Errorf("client id(%s) not found", id)
		return nil, err
	}
	return cli, nil
}

func (c *ClientCache) setClient(id string, client *gophercloud.ServiceClient) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.clientMap[id] = client
}

func (c *ClientCache) delClient(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.clientMap, id)
}
