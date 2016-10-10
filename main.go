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
	var pc *proxyConfig
	if err = json.NewDecoder(f).Decode(&pc); err != nil {
		log.Fatalf("error decoding config.json: %v", err)
	}
	f.Close()

	p, err := newProxy(pc)
	if err != nil {
		log.Fatal(err)
	}
	log.Fatal(p.listenAndServe())
}
