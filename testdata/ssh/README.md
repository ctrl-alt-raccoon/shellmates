# Disposable Linux SSH regression

This opt-in test runs a private OpenSSH server bound only to container loopback,
with newly generated fixture keys. All remote commands run as `sclaude-test`
(UID 1000), with temporary HOME/XDG/Screen directories and a fake backend.
No vendor CLI, credentials, host SSH service, or published port is used.

Build the test image before running verification:

```sh
docker build -f testdata/ssh/Dockerfile -t sclaude-ssh-check .
docker run --rm --init --network none --read-only --pids-limit 256 --memory 3g \
  --tmpfs /tmp:rw,exec,nosuid,size=2g \
  --tmpfs /home/sclaude-test:rw,exec,nosuid,size=64m,uid=1000,gid=1000,mode=700 \
  --mount type=bind,source="$PWD",target=/src,readonly \
  -e HOME=/tmp/check/home -e XDG_CONFIG_HOME=/tmp/check/config \
  -e XDG_STATE_HOME=/tmp/check/state -e XDG_DATA_HOME=/tmp/check/data \
  -e GOCACHE=/tmp/check/build -e GOMODCACHE=/go/pkg/mod \
  -e GOTOOLCHAIN=local -e GOPROXY=off -e SCLAUDE_SSH_INTEGRATION=1 \
  sclaude-ssh-check go test -race -count=1 -v -run '^TestLinuxSSHReconnect$' ./internal/app
```

The test requires container root only for sshd privilege separation. Do not run
it as root on a real host. It checks detached creation, two abrupt transport
disconnects, reattachment and terminal input to the same backend process,
acknowledged stop, process exit, and prune. It does not simulate a silent
half-open TCP connection or certify real vendor CLI behavior.
