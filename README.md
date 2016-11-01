# tlsmuxd

## Example config
```json
{
	"email": "user\@example.com",
	"cacheDir": "/var/lib/tlsmuxd",
	"hosts": {
		"example.com": [{
			"name": "ssh",
			"addr": "localhost:906"
		}, {
			"name": "h2",
			"addr": "localhost:8080"
		}, {
			"name": "http/1.1",
			"addr": "localhost:8081"
		}, {
			"name": "",
			"addr": "localhost:8081"
		}],
		"www.example.com": [{
			"name": "h2",
			"addr": "localhost:8080"
		}, {
			"name": "http/1.1",
			"addr": "localhost:8081"
		}, {
			"name": "",
			"addr": "localhost:8081"
		}]
	}
}
```

TODO:
-----
- [ ] Tests
- [ ] Docs
