# Codec backends

The default build has no native codec dependency. PCMU, PCMA, G.722, and iLBC are available in a normal `go build`.

GSM-FR, Opus, and standalone SILK use optional CGO backends. Enable them only when a C compiler is available; GSM-FR and Opus also require the matching development packages:

```bash
go test -tags "gsm opus silk" ./media ./client
go build -tags "gsm opus silk" ./...
```

On Debian and Ubuntu, install:

```bash
sudo apt-get install libgsm1-dev libogg-dev libopus-dev libopusfile-dev pkg-config
```

| Codec | Go/native backend | Build tag | Distribution terms |
| --- | --- | --- | --- |
| G.722 | `github.com/gotranspile/g722` | none | See the dependency license in the module source |
| iLBC | `github.com/Bigous/iLBC-Go` | none | See the dependency license in the module source |
| GSM-FR | system `libgsm` | `gsm` with CGO | `tu-berlin-2.0` in Fedora package metadata |
| Opus | `gopkg.in/hraban/opus.v2` plus system `libopus`/`libopusfile` | `opus` with CGO | Go wrapper MIT; native Opus BSD-3-Clause/BSD-2-Clause |
| SILK | `github.com/youthlin/silk` with embedded Skype SILK SDK sources | `silk` with CGO | Go wrapper MIT; embedded SDK uses its three-clause notice and explicitly grants no patent rights |

The client advertises GSM-FR, Opus, or SILK only when its backend was compiled into the binary. The CI job builds and runs codec vectors with all three tags, while the cross-platform matrix continues to validate the dependency-free build. SILK remains a separate API and RTP format; enabling Opus alone never advertises SILK.
