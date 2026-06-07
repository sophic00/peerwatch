# Contributing to PeerWatch

## Commit Message Guidelines

Our commit message format is based on the [Scoped Commits](https://scopedcommits.com) specification.

From now on, all commits should use the following format:

```
<scope>: <description>

[optional body]

[optional trailer(s)]
```

### Components

1. **`<scope>`**
   Represents the section, package, or component of the codebase that the commit modifies. Common scopes include:
   - `scheduler` (scheduler logic and download orchestration)
   - `swarm` (peer connection loop and TCP mesh manager)
   - `chunk` (store, chunker, manifest builder)
   - `player` (mpv control, HTTP range server)
   - `protocol` (wire protocol codecs, messages, handlers)
   - `sync` (playback synchronization)
   - `docs` (updates to architecture, types, protocol docs, etc.)
   - `build` (Go dependencies, build configuration)

2. **`<description>`**
   A short, imperative-mood summary of the changes (e.g., use "add" instead of "added", "fix" instead of "fixes").
   - *Example:* `scheduler: add rarest-first priority strategy`

3. **`[optional body]`**
   Provides detailed context or rationale behind the change. Separate it from the subject line with a blank line.

4. **`[optional trailer(s)]`**
   Used to reference issues, list breaking changes, or note co-authors.
   - *Example:* `Fixes #12`

### Sample Commit Messages

#### Single-line commit:
```git
scheduler: prefer chunks within playback window
```

#### Detailed multi-line commit:
```git
swarm: close duplicate peer connections

When we receive an incoming connection from a peer that already has an
established outgoing connection, reject the duplicate to prevent resource
leaks and dual-direction message processing.

Fixes #28
```

## Development and Testing Guidelines

To ensure the P2P swarm logic and chunk scheduler remain robust and correct, follow these guidelines when writing and testing code.

### Code Style and Formatting
- Format all Go code using `go fmt`:
  ```bash
  go fmt ./...
  ```
- Run static analysis checks:
  ```bash
  go vet ./...
  ```

### Running the Test Suite
- Run all package tests locally:
  ```bash
  go test ./... -v
  ```
- To detect concurrency bugs or data races in goroutines, always run tests with the race detector enabled:
  ```bash
  go test -race ./... -v
  ```

### Local P2P Swarm Manual Testing
Since PeerWatch is a peer-to-peer system, verify your changes behave correctly under actual mesh network conditions by running a local multi-peer test swarm:

1. **Build the CLI binary:**
   ```bash
   go build -o peerwatch .
   ```

2. **Start the Host Node** in a terminal window, specifying a sample video file:
   ```bash
   ./peerwatch start movie.mp4
   ```
   *Note: This will print a base64-encoded token to the console.*

3. **Join as Peer Nodes** in separate terminal windows, specifying distinct output directories to prevent files overwriting each other:
   ```bash
   # In terminal 2:
   ./peerwatch join -out ./peer2_data "<token>"

   # In terminal 3:
   ./peerwatch join -out ./peer3_data "<token>"
   ```

4. **Verify Swarm Behavior:**
   - Check that peers successfully build a full mesh (all connect to each other).
   - Verify that chunks are transferred and verified correctly.
   - Verify that playback in `mpv` remains synchronized across nodes.

