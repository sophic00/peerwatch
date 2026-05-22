# PeerWatch Security & Threat Model

PeerWatch is a serverless, peer-to-peer CLI application designed for small, trusted groups (e.g., 5-10 friends) to watch videos together. Because it uses raw TCP connections in a full-mesh topology without a central server or cryptographic identities, users must understand the security assumptions and threat model of the system.

---

## 1. Trust Assumptions

- **Host Trust**: Joining peers must trust the room Host. The host defines the file name, file size, and the SHA-256 chunk hashes that all joining peers will download and play.
- **Peer Trust**: Because all peers form a full mesh, they connect directly to each other. Every participant in a room must trust all other participants not to behave maliciously.
- **Environment Trust**: The CLI runs standard external command execution to launch `mpv`. The system assumes the local binary environment is secure and that `mpv` is trusted.

---

## 2. Threat Model & Key Concerns

### A. IP Address Exposure (Full-Mesh Network)
- **Threat**: A participant's IP address and port are exposed to other users.
- **Mechanism**: PeerWatch utilizes a full-mesh topology. When joining, a peer connects to the Host, receives a list of other peer IP addresses (`PEER_LIST`), and immediately dials every peer directly.
- **Impact**: Any user who obtains the room connection token can see the IP addresses of all connected peers. This could lead to targeted DDoS attacks or geographic tracking if tokens are shared publicly.
- **Mitigation**: Only share room connection tokens over private, trusted communication channels. Do not post tokens on public forums.

### B. Untrusted Media Files & Remote Code Execution (RCE)
- **Threat**: A malicious host shares a crafted video file containing a parser exploit.
- **Mechanism**: Peers automatically download video chunks, write them to a local `.partial` file, and stream the file to a local HTTP server that `mpv` reads via Range requests.
- **Impact**: Video parsers and decoders (e.g., FFmpeg, which powers `mpv`) have a historically large attack surface. If a malicious host serves a specially crafted malformed video file, parsing it in `mpv` could trigger a buffer overflow or other vulnerability, leading to arbitrary code execution on the joining peer's machine.
- **Mitigation**: 
  - **Never join rooms hosted by untrusted parties.**
  - Ensure your system's `mpv` and underlying media libraries (FFmpeg) are kept up to date with the latest security patches.
  - Run the CLI in a sandboxed or containerized environment if playing videos from semi-trusted hosts.

### C. Unencrypted Network Traffic
- **Threat**: Network eavesdroppers can inspect and rebuild the video stream.
- **Mechanism**: The wire protocol operates over raw, unencrypted TCP connections without TLS.
- **Impact**: A man-in-the-middle (MITM) or network administrator on the same network path can capture the TCP packets, reconstruct the video chunks, track playback seek/pause states, and monitor room metadata.
- **Mitigation**: Avoid using PeerWatch on public/unsecured Wi-Fi networks. If encryption is required, users can tunnel connections through a secure VPN or SSH tunnel.

### D. Token Security & Access Control
- **Threat**: An unauthorized user joins the watch party.
- **Mechanism**: Room tokens are base64url-encoded JSON payloads (`pw_<payload>`) containing the host's IP/port, room ID, and file metadata. They are unencrypted and unsigned.
- **Impact**: Anyone who intercepts or receives a copy of the token can instantly join the swarm, receive the manifest, download the video, and view connected peers. There is no password protection or handshake authentication.
- **Mitigation**: Treat room tokens like temporary passwords. Share them exclusively via end-to-end encrypted messaging with intended participants.

### E. Lack of Digital Signatures (Manifest Spoofing)
- **Threat**: A man-in-the-middle alters the manifest or redirects a joining peer.
- **Mechanism**: The connection token and the subsequent `MANIFEST` wire message are not digitally signed.
- **Impact**: While PeerWatch guarantees *integrity* (chunks downloaded are verified against the manifest SHA-256 hashes), it does not guarantee *authenticity*. A MITM attacker could intercept the TCP connection and substitute a different manifest or file without the peer knowing, provided they can spoof the connection.
- **Mitigation**: This threat is mitigated by the host trust model and secure token delivery, but remain aware that the protocol itself does not utilize public-key cryptography.

### F. Resource Exhaustion & Denial of Service (DoS)
- **Threat**: A malicious peer exhausts another peer's disk, bandwidth, or CPU.
- **Mechanism**: 
  - PeerWatch allocates a sparse file equal to the `FileSize` specified in the manifest. A malicious host could specify an extremely large size to exhaust peer disk space.
  - There are no rate limits, request throttling, or validation of incoming batch sizes.
- **Impact**: A malicious peer can flood a connection with oversized requests or garbage messages, causing high CPU load, network saturation, or crash conditions.
- **Mitigation**: Since the connection is serverless, the only mitigation if a peer behaves maliciously is to manually terminate the CLI process.
