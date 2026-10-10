package config

import (
	"encoding/base64"
	"testing"
)

func TestMultiConfigIndependentCredentials(t *testing.T) {
	key := func(b byte) string {
		data := make([]byte, 32)
		for i := range data {
			data[i] = b
		}
		return base64.StdEncoding.EncodeToString(data)
	}
	base := Config{Mode: "server", Listen: "127.0.0.1:443", WireGuard: "127.0.0.1:51820", Transport: "exchange", Profile: "balanced", Key: key(1)}
	c := MultiServer{Mode: "server-multi", Listen: base.Listen, MaxPPS: 8000, Users: []ServerUser{{Name: "alice", Config: base}}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	second := base
	second.Key = key(2)
	c.Users = append(c.Users, ServerUser{Name: "bob", Config: second})
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*MultiServer){
		func(c *MultiServer) { c.Users[1].Config.Key = c.Users[0].Config.Key },
		func(c *MultiServer) { c.Users[1].Name = c.Users[0].Name },
		func(c *MultiServer) { c.Users[1].Name = "../../root" },
		func(c *MultiServer) { c.Users[1].Config.Transport = "raw" },
		func(c *MultiServer) { c.Users[1].Config.Listen = "127.0.0.1:444" },
		func(c *MultiServer) { c.MaxPPS = 12001 },
	} {
		bad := c
		bad.Users = append([]ServerUser(nil), c.Users...)
		change(&bad)
		if bad.Validate() == nil {
			t.Fatal("unsafe multi-user configuration accepted")
		}
	}
	c.Users = nil
	if err := c.Validate(); err != nil {
		t.Fatal("revoked last user must leave an empty server", err)
	}
}
