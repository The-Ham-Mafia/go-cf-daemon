# go-cf-daemon

A simple Go DDNS daemon for Cloudflare.

## Installation

### Download (Recommended)

Grab the latest binary from the [Releases](https://github.com/The-Ham-Mafia/go-cf-daemon/releases) page.

### Build from Source

Requires [Go](https://go.dev/dl/) to be installed.

```bash
git clone https://github.com/The-Ham-Mafia/go-cf-daemon.git
cd go-cf-daemon
go build -o gcfd .
```

## Configuration

Copy `example.toml` and fill in your details:

```bash
cp example.toml config.toml
```

**Example config:**

```toml
poll_interval = 300 # seconds
cloudflare_api_token = "your_cloudflare_token"
ip_provider = "api.ipify.org"

[[zone]]
name = "example.com"
records = [
  { name = "@", proxied = true },
  { name = "www", type = "CNAME", target = "example.com", proxied = true },
]

[[zone]]
name = "another-domain.com"
records = [
  { name = "@", proxied = false },
]
```

Multiple zones are supported by repeating the `[[zone]]` block. Each record can specify a `type` (defaults to `A`), a `target` for non-A/AAAA records, and whether it should be `proxied` through Cloudflare.

## Getting a Cloudflare API Token

1. Log in to the [Cloudflare Dashboard](https://dash.cloudflare.com)
2. Go to **Manage Account** → **Account API Tokens** → **Create Token**
3. Use the **Edit zone DNS** template, or create a custom token with `Zone > DNS > Edit` permissions
4. When selecting zone access, choose **all zones** or **specific zones**. Make sure it covers every zone defined in your config

## Running

By default, the binary looks for `config.toml` in the same directory. You can optionally pass a custom path:

```bash
./gcfd                        # uses ./config.toml
./gcfd /path/to/config.toml   # uses specified path
```

## Running as a systemd Service (Linux)

An example service file is included in the repo (`gcfd.service`).

1. Edit `gcfd.service` to set the correct paths for your system.
2. Copy it to the systemd directory and enable it:

```bash
sudo cp gcfd.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now gcfd
```

Check status with:

```bash
sudo systemctl status gcfd
```

## License

MIT
