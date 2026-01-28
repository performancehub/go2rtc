# Multistream WebRTC Module

The multistream module enables **instant camera switching** over a single WebRTC connection. Instead of creating separate connections for each camera (which causes 2-5 second delays when switching), this module allows the frontend to dynamically switch between streams without any reconnection.

## Key Benefits

- **Single Connection**: One WebRTC peer connection handles multiple camera streams
- **Instant Switching**: Switch cameras in <300ms (vs 2-5 seconds with iframe approach)
- **Reduced Device Load**: 1 connection instead of 6 for a 6-camera view
- **Quality Switching**: Change stream quality without reconnection
- **Status Updates**: Real-time notifications for stream status changes

## Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│  Frontend (Browser)                                                 │
│  ┌───────────────────────────────────────────────────────────────┐  │
│  │  MultiStreamClient                                             │  │
│  │  - Single RTCPeerConnection with N video tracks               │  │
│  │  - WebSocket control channel                                   │  │
│  │  - Maps track.mid → <video> element                           │  │
│  └───────────────────────────────────────────────────────────────┘  │
│                              │                                      │
│              WebSocket: /api/ws (multistream/* messages)            │
│                              │                                      │
└──────────────────────────────┼──────────────────────────────────────┘
                               │
┌──────────────────────────────┼──────────────────────────────────────┐
│  go2rtc (Device)             │                                      │
│  ┌───────────────────────────▼───────────────────────────────────┐  │
│  │  MultiStreamSession                                           │  │
│  │  ┌─────────────────────────────────────────────────────────┐  │  │
│  │  │  Slot 0 → [Track] ←─ Stream "camera1-360p"              │  │  │
│  │  │  Slot 1 → [Track] ←─ Stream "camera2-360p"              │  │  │
│  │  │  Slot 2 → [Track] ←─ Stream "camera3-360p"              │  │  │
│  │  └─────────────────────────────────────────────────────────┘  │  │
│  │                                                               │  │
│  │  PeerConnection (single) ─── ICE/DTLS ─── Browser             │  │
│  └───────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────┘
```

## Configuration

```yaml
multistream:
  max_slots: 9  # Maximum slots per connection (default: 9)
```

## WebSocket Protocol

All messages are JSON objects sent over the existing `/api/ws` WebSocket endpoint.

### Client → Server Messages

#### 1. Initialize Session

Request multiple stream slots:

```json
{
  "type": "multistream/init",
  "request_id": "uuid-123",
  "slots": [
    { "slot": 0, "stream": "camera1", "quality": "360p" },
    { "slot": 1, "stream": "camera2", "quality": "360p" },
    { "slot": 2, "stream": "camera3", "quality": "360p" }
  ]
}
```

#### 2. Send WebRTC Offer

After receiving `multistream/ready`, send the SDP offer:

```json
{
  "type": "multistream/offer",
  "request_id": "uuid-456",
  "sdp": "v=0\r\no=- ..."
}
```

#### 3. Switch Stream (No Reconnection!)

Switch a slot to a different camera:

```json
{
  "type": "multistream/switch",
  "request_id": "uuid-789",
  "slot": 0,
  "stream": "camera4",
  "quality": "720p"
}
```

#### 4. ICE Candidates

```json
{
  "type": "multistream/ice",
  "candidate": "candidate:1 1 UDP ..."
}
```

#### 5. Close Session

```json
{
  "type": "multistream/close"
}
```

### Server → Client Messages

#### 1. Session Ready

Sent after init, before offer:

```json
{
  "type": "multistream/ready",
  "request_id": "uuid-123",
  "slots": 3
}
```

#### 2. WebRTC Answer

```json
{
  "type": "multistream/answer",
  "request_id": "uuid-456",
  "sdp": "v=0\r\no=- ...",
  "slots": [
    { "slot": 0, "stream": "camera1-360p", "status": "active" },
    { "slot": 1, "stream": "camera2-360p", "status": "active" },
    { "slot": 2, "stream": "camera3-360p", "status": "error", "error": "stream not found" }
  ]
}
```

#### 3. Status Update

Sent after stream switch or when status changes:

```json
{
  "type": "multistream/status",
  "slot": 0,
  "status": {
    "slot": 0,
    "stream": "camera4-720p",
    "status": "active"
  }
}
```

#### 4. ICE Candidates

```json
{
  "type": "multistream/ice",
  "value": "candidate:1 1 UDP ..."
}
```

### Status Values

| Status | Description |
|--------|-------------|
| `pending` | Slot created, not yet bound |
| `active` | Stream is playing |
| `buffering` | Switching streams, waiting for data |
| `offline` | Stream source is offline |
| `error` | Error occurred (see `error` field) |
| `inactive` | Slot unbound from stream |

## Stream Naming Convention

Quality variants are configured as separate streams with naming pattern:

```yaml
streams:
  camera1: rtsp://192.168.1.10/stream
  camera1-360p: ffmpeg:camera1#video=h264#height=360
  camera1-720p: ffmpeg:camera1#video=h264#height=720
  camera1-1080p: ffmpeg:camera1#video=h264#height=1080
```

The multistream module resolves stream names using the pattern:
- `stream: "camera1", quality: ""` → `"camera1"`
- `stream: "camera1", quality: "native"` → `"camera1"`
- `stream: "camera1", quality: "360p"` → `"camera1-360p"`

## Frontend Integration

### TypeScript Client

A TypeScript client is provided at `www/multistream-client.ts`:

```typescript
import { MultiStreamClient } from './multistream-client';

const client = new MultiStreamClient('https://device.example.com');

// Connect with 6 cameras
const slots = cameras.map((cam, i) => ({
  slot: i,
  stream: cam.id,
  quality: '360p',
}));

const trackMap = await client.connect(slots, (slot, status) => {
  console.log(`Slot ${slot}: ${status.status}`);
});

// Attach tracks to video elements
trackMap.forEach((track, slot) => {
  const video = document.getElementById(`video-${slot}`);
  video.srcObject = new MediaStream([track]);
  video.play();
});

// Switch camera (instant!)
await client.switchStream(0, 'camera7', '1080p');
```

### React Example

```tsx
import { useEffect, useRef, useState } from 'react';
import { MultiStreamClient, SlotConfig, SlotStatus } from './multistream-client';

function CameraGrid({ deviceUrl, cameras }) {
  const clientRef = useRef<MultiStreamClient | null>(null);
  const videoRefs = useRef<Map<number, HTMLVideoElement>>(new Map());
  const [statuses, setStatuses] = useState<Map<number, SlotStatus>>(new Map());

  useEffect(() => {
    const client = new MultiStreamClient(deviceUrl);
    clientRef.current = client;

    const slots: SlotConfig[] = cameras.map((cam, i) => ({
      slot: i,
      stream: cam.id,
      quality: '360p',
    }));

    client.connect(slots, (slot, status) => {
      setStatuses(prev => new Map(prev).set(slot, status));
    }).then(trackMap => {
      trackMap.forEach((track, slot) => {
        const video = videoRefs.current.get(slot);
        if (video) {
          video.srcObject = new MediaStream([track]);
          video.play().catch(() => {
            video.muted = true;
            video.play();
          });
        }
      });
    });

    return () => client.disconnect();
  }, [deviceUrl, cameras]);

  const handleClick = (slot: number, cameraId: string) => {
    clientRef.current?.switchStream(slot, cameraId, '1080p');
  };

  return (
    <div className="grid grid-cols-3 gap-2">
      {cameras.map((cam, slot) => (
        <div key={cam.id} onClick={() => handleClick(slot, cam.id)}>
          <video
            ref={el => el && videoRefs.current.set(slot, el)}
            autoPlay
            playsInline
            muted
          />
          <span>{statuses.get(slot)?.status}</span>
        </div>
      ))}
    </div>
  );
}
```

## Migration from Iframes

### Before (iframe approach)

```tsx
// Each camera = separate iframe = separate WebRTC connection
{cameras.map(cam => (
  <iframe
    key={cam.id}
    src={`${deviceUrl}/stream.html?src=${cam.id}`}
  />
))}
```

### After (multistream approach)

```tsx
// Single connection for all cameras
<CameraGrid deviceUrl={deviceUrl} cameras={cameras} />
```

### Gradual Migration

Use feature flags to gradually roll out:

```tsx
function StreamViewer({ device, cameras }) {
  const useMultistream = useFeatureFlag('multistream-enabled');

  if (useMultistream) {
    return <CameraGrid deviceUrl={device.url} cameras={cameras} />;
  }

  // Fallback to iframes
  return (
    <div>
      {cameras.map(cam => (
        <iframe key={cam.id} src={`${device.url}/stream.html?src=${cam.id}`} />
      ))}
    </div>
  );
}
```

## Performance Comparison

| Metric | Iframe Approach | Multistream |
|--------|-----------------|-------------|
| Connection time (6 cameras) | 6-12 seconds | <2 seconds |
| Camera switch time | 2-5 seconds | <300ms |
| WebRTC connections | 6 | 1 |
| Device CPU overhead | High | ~50% less |
| Browser memory | High (6 iframes) | ~40% less |

## Limitations

- All streams in a session must use compatible codecs (H264 recommended)
- Maximum slots per connection is configurable (default: 9)
- Audio is per-track (no mixing) - typically muted in grid views

## Troubleshooting

### Stream Not Found

Ensure the stream is configured in go2rtc:

```yaml
streams:
  camera1: rtsp://...
  camera1-360p: ffmpeg:camera1#video=h264#height=360
```

### ICE Connection Failed

Check network/firewall settings. WebRTC requires:
- STUN server access (default: stun.l.google.com:19302)
- UDP ports for media (or TCP fallback)

### Codec Mismatch

Ensure all quality variants use the same codec:

```yaml
streams:
  camera1-360p: ffmpeg:camera1#video=h264#height=360
  camera1-720p: ffmpeg:camera1#video=h264#height=720  # Same codec
```
