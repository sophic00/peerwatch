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
