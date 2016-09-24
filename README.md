# tlsmuxd

## Example config
```toml
hostnames = [
	"example.com",
	"www.example.com",
	"avondieselemission.com",
	"www.avondieselemission.com"
]
email = "foo@example.com"
[protos.ssh.default]
	name  = "ssh"
	addr  = "localhost:18187"
[protos.openvpn.default]
	name  = "openvpn"
	addr  = "localhost:1194"
[fallback.hosts."avondieselemission.com"]
	name  = "ADE"
	addr  = "localhost:8081"
[fallback.hosts."www.avondieselemission.com"]
	name  = "ADE"
	addr  = "localhost:8081"
[fallback.default]
	name  = "http"
	addr  = "localhost:8080"
```

TODO:
-----
- ~~[x] Prioritize protocol or servername? (prioritize protocol)~~
- [x] Should an empty protocol be allowed? (yes) (clients do not always support)
- ~~[ ] sniff or dialTLS?``~~ (no need)
- ~~[ ] x-forwarded-for?~~ (too much trouble)
- [x] Should default backends be forced? (yes)
- [ ] How to handle priority?
- [ ] Load balancing (custom algorithm)
- [ ] wildcards in servers, not cert hosts.
- [ ] Embed slices with `toml:"."`
