package main

import (
	"encoding/json"
	"flag"
	"os"

	"github.com/nhooyr/log"
)

func main() {
	configPath := flag.String("c", "", "path to configuration file")
	flag.Parse()

	f, err := os.Open(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	var c *config
	if err = json.NewDecoder(f).Decode(&c); err != nil {
		log.Fatalf("error decoding config.json: %v", err)
	}
	f.Close()

	p, err := newProxy(c)
	if err != nil {
		log.Fatal(err)
	}
	log.Fatal(p.listenAndServe())
}
