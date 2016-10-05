# tlsmuxd

## Example config
```json
{
	"bindInterfaces": [
		"example.com"
	],
	"email": "user@example.com",
	"cacheDir": "/var/lib/tlsmuxd",
	"protos": [{
		"name": "ssh",
		"hosts": {
			"example.com": "localhost:906"
		}
	}, {
		"name": "h2",
		"hosts": {
			"example.com": "localhost:8080",
			"www.example.com": "localhost:8080",
			"example2.com": "localhost:8081",
			"www.example2.com": "localhost:8081"
		}
	}, {
		"name": "http/1.1",
		"hosts": {
			"example.com": "localhost:8083",
			"www.example.com": "localhost:8083",
			"example2.com": "localhost:8084",
			"www.example2.com": "localhost:8084"
		}
	}],
		"defaultProto": "http/1.1"
}
```

TODO:
-----
- [ ] Tests
- [ ] Docs
- [ ] Unix domain sockets
