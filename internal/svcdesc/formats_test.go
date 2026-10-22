package svcdesc

import "testing"

func evalFmt(t *testing.T, format string, opt FormatOptions, data, path string) string {
	t.Helper()
	root, err := Decode(format, []byte(data), opt)
	if err != nil {
		t.Fatalf("%s: %v", format, err)
	}
	p, err := CompilePath(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []any
	for _, n := range p.Eval(root, root) {
		out = append(out, Plain(n.Value))
	}
	return asJSON(out)
}

func TestFormats(t *testing.T) {
	cases := []struct {
		name, format string
		opt          FormatOptions
		data, path   string
		want         string
	}{
		{"yaml anchors", FormatYAML, FormatOptions{}, "base: &b\n  port: 80\nweb:\n  <<: *b\n  host: x\n", "$.web.port", `[80]`},
		{"yaml multi-doc", FormatYAML, FormatOptions{}, "a: 1\n---\na: 2\n", "$[*].a", `[1,2]`},
		{"json order", FormatJSON, FormatOptions{}, `{"z":1,"a":2}`, "$.*", `[1,2]`},
		{"toml", FormatTOML, FormatOptions{}, "[entryPoints.web]\naddress = \":80\"\n[entryPoints.websecure]\naddress = \":443\"\n", "$.entryPoints.*.address", `[":80",":443"]`},
		{"ini", FormatINI, FormatOptions{}, "[mysqld]\nport = 3306\nbind-address = 0.0.0.0 ; all\nskip-name-resolve\n[client]\nport=3307\n", "$.mysqld", `[{"bind-address":"0.0.0.0","port":"3306","skip-name-resolve":true}]`},
		{"env", FormatEnv, FormatOptions{}, "# c\nexport PORT=8080\nNAME=\"a b\" # c\nX='y'\n", "$.*", `["8080","a b","y"]`},
		{"properties", FormatProperties, FormatOptions{}, "# c\nlisteners=PLAINTEXT://:9092,\\\n  SSL://:9093\nlog.dirs : /var/kafka\nname\\ x value\n", "$.*", `["PLAINTEXT://:9092,SSL://:9093","/var/kafka","value"]`},
		{"lines redis", FormatLines, FormatOptions{}, "bind 127.0.0.1 -::1\nport 6379\nsave 900 1\nsave 300 10\nrequirepass \"p#ss\"\n", "$.save", `[["900 1","300 10"]]`},
		{"lines postgres", FormatLines, FormatOptions{}, "listen_addresses = '*'   # what IP\nport = 5432\n", "$.listen_addresses", `["*"]`},
		{"lines mosquitto blocks", FormatLines, FormatOptions{Blocks: []string{"listener"}}, "per_listener_settings true\nlistener 1883 127.0.0.1\nallow_anonymous true\nlistener 8883\ncafile /etc/ca.pem\n", "$.listener[*]['@value']", `["1883 127.0.0.1","8883"]`},
		{"lines sshd lower", FormatLines, FormatOptions{LowerKeys: true}, "PermitRootLogin no\nPort 22\n", "$.permitrootlogin", `["no"]`},
		{"xml", FormatXML, FormatOptions{}, `<Server port="8005"><Service name="Catalina"><Connector port="8080" protocol="HTTP/1.1"/><Connector port="8443" SSLEnabled="true"/></Service></Server>`, "$.Server.Service.Connector[*]['@port']", `["8080","8443"]`},
		{"xml text", FormatXML, FormatOptions{}, `<configuration><property><name>fs.defaultFS</name><value>hdfs://nn:8020</value></property></configuration>`, "$..value['#text']", `["hdfs://nn:8020"]`},
		{"regex", FormatRegex, FormatOptions{Pattern: `listen\s+(?P<port>\d+)`}, "listen 80\nlisten 443\n", "$[*].port", `["80","443"]`},
		{"text", FormatText, FormatOptions{}, "hello", "$", `["hello"]`},
	}
	for _, c := range cases {
		if got := evalFmt(t, c.format, c.opt, c.data, c.path); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}
