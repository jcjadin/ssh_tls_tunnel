# tlsmuxd

## Example config
```toml
hostnames = [
	"example.com",
	"www.example.com",
	"hooli.com",
	"www.hooli.com"
]
email = "me@example.com"
[[backends]]
	name        = "ssh"
	addr        = "localhost:18187"
	protocols   = ["ssh"]
[[backends]]
	name        = "openvpn"
	addr        = "localhost:1194"
	protocols   = ["openvpn"]
[[backends]]
	name        = "hooli"
	addr        = "localhost:8081"
	serverNames = ["hooli.com", "www.hooli.com"]
[fallback]
	name        = "http"
	addr        = "localhost:8080"
```

TODO:
-----
- [ ] Prioritize protocol or servername?
- [ ] How much validation to do?
