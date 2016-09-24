# tlsmuxd

## Example config
```json
{
	"hosts": [
		"example.com",
		"www.example.com"
	],
	"email": "user@example.com",
	"default": {
		"fallback": "localhost:8083",
		"hosts": {
			"example2.com": "localhost:8084",
			"www.example2.com": "localhost:8084"
		}
	},
	"protos": [{
		"name": "ssh",
		"fallback": "localhost:906"
	}, {
		"name": "h2",
		"fallback": "localhost:8080",
		"hosts": {
			"example2.com": "localhost:8081",
			"www.example2.com": "localhost:8081"
		}
	}, {
		"name": "http/1.1",
		"fallback": "localhost:8083",
		"hosts": {
			"example2.com": "localhost:8084",
			"www.example2.com": "localhost:8084"
		}
	}]
}
```

TODO:
-----
- ~~[x] Prioritize protocol or servername? (prioritize protocol)~~
- [x] Should an empty protocol be allowed? (yes) (clients do not always support)
- ~~[ ] sniff or dialTLS?``~~ (no need)
- ~~[ ] x-forwarded-for?~~ (too much trouble)
- [x] Should default backends be forced? (yes)
- [ ] Load balancing (custom algorithm)
- [ ] wildcards in servers, not cert hosts.
