# Multistream WebRTC Module

The multistream module enables **instant camera switching** over a single WebRTC connection. Instead of creating separate connections for each camera (which causes 2-5 second delays when switching), this module allows the frontend to dynamically switch between camera streams without any reconnection.

## Table of Contents

- [Key Benefits](#key-benefits)
- [Architecture](#architecture)
- [Configuration](#configuration)
- [Stream Setup](#stream-setup)
- [WebSocket Protocol](#websocket-protocol)
  - [Connection Flow](#connection-flow)
  - [Message Reference](#message-reference)
  - [Status Values](#status-values)
- [Frontend Integration](#frontend-integration)
  - [TypeScript Client](#typescript-client)
  - [React Integration](#react-integration)
  - [Vue Integration](#vue-integration)
  - [Vanilla JavaScript](#vanilla-javascript)
- [Migration from Iframes](#migration-from-iframes)
- [Performance Comparison](#performance-comparison)
- [Automatic Connection Monitoring](#automatic-connection-monitoring)
- [Error Handling](#error-handling)
- [Troubleshooting](#troubleshooting)
- [API Reference](#api-reference)
- [Limitations](#limitations)

---

## Key Benefits

| Benefit | Description |
|---------|-------------|
| **Single Connection** | One WebRTC peer connection handles multiple camera streams |
| **Instant Switching** | Switch cameras in <300ms (vs 2-5 seconds with iframe approach) |
| **Reduced Device Load** | 1 connection instead of 6 for a 6-camera grid view |
| **Quality Switching** | Change stream quality without reconnection |
| **Status Updates** | Real-time notifications for stream status changes |
| **Memory Efficiency** | No iframe DOM overhead (~40% less browser memory) |
| **Simplified State** | Single connection state to manage |

---

## Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│  Frontend (Browser)                                                 │
│                                                                     │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │  MultiStreamClient                                           │   │
│  │                                                              │   │
│  │  ┌──────────────────┐    ┌─────────────────────────────┐   │   │
│  │  │  WebSocket       │    │  RTCPeerConnection          │   │   │
│  │  │  Control Channel │    │  - Track 0 → Video Element 1│   │   │
│  │  │  (JSON messages) │    │  - Track 1 → Video Element 2│   │   │
│  │  └────────┬─────────┘    │  - Track 2 → Video Element 3│   │   │
│  │           │              │  - ...                      │   │   │
│  │           │              └──────────────┬──────────────┘   │   │
│  └───────────┼──────────────────────────────┼─────────────────┘   │
│              │                              │                      │
└──────────────┼──────────────────────────────┼──────────────────────┘
               │ WebSocket                    │ WebRTC (ICE/DTLS)
               │ /api/ws                      │
               ▼                              ▼
┌──────────────────────────────────────────────────────────────────────┐
│  go2rtc Device                                                       │
│                                                                      │
│  ┌────────────────────────────────────────────────────────────────┐ │
│  │  Multistream Module                                            │ │
│  │                                                                 │ │
│  │  ┌─────────────────────────────────────────────────────────┐  │ │
│  │  │  MultiStreamSession                                      │  │ │
│  │  │                                                          │  │ │
│  │  │  ┌──────────┐ ┌──────────┐ ┌──────────┐                │  │ │
│  │  │  │  Slot 0  │ │  Slot 1  │ │  Slot 2  │ ...            │  │ │
│  │  │  │          │ │          │ │          │                │  │ │
│  │  │  │ Track    │ │ Track    │ │ Track    │                │  │ │
│  │  │  │ Consumer │ │ Consumer │ │ Consumer │                │  │ │
│  │  │  └────┬─────┘ └────┬─────┘ └────┬─────┘                │  │ │
│  │  │       │            │            │                       │  │ │
│  │  │       │   SWITCH   │   SWITCH   │  ← Instant!          │  │ │
│  │  │       ▼            ▼            ▼                       │  │ │
│  │  └───────────────────────────────────────────────────────────┘  │ │
│  └────────────────────────────────────────────────────────────────┘ │
│                         │            │            │                  │
│  ┌──────────────────────┼────────────┼────────────┼───────────────┐ │
│  │  Streams Module      ▼            ▼            ▼               │ │
│  │                                                                 │ │
│  │  ┌─────────────┐ ┌─────────────┐ ┌─────────────┐              │ │
│  │  │ camera1     │ │ camera2     │ │ camera3     │              │ │
│  │  │ camera1-360p│ │ camera2-720p│ │ camera3-1080│              │ │
│  │  │ camera1-720p│ │ ...         │ │ ...         │              │ │
│  │  └─────────────┘ └─────────────┘ └─────────────┘              │ │
│  └────────────────────────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────────────────────┘
```

**Data Flow for Stream Switch:**

```
1. Frontend: client.switchStream(0, 'camera2', '720p')
2. WebSocket: { type: "multistream/switch", slot: 0, stream: "camera2", quality: "720p" }
3. Server: Slot 0 unbinds from camera1-360p
4. Server: Slot 0 binds to camera2-720p
5. Server: Video data flows through existing WebRTC track
6. WebSocket: { type: "multistream/status", slot: 0, status: "active" }
7. Frontend: Video element shows new camera (no reconnection!)
```

---

## Configuration

Add to your `go2rtc.yaml`:

```yaml
multistream:
  max_slots: 9  # Maximum slots per connection (default: 9)
```

The `max_slots` setting limits how many concurrent video streams a single client can request. The default of 9 accommodates common grid layouts (2x2, 3x3, 2x3).

---

## Stream Setup

### Quality Variants

For instant quality switching, configure quality variants as separate streams using FFmpeg transcoding:

```yaml
streams:
  # Native RTSP stream
  camera1: rtsp://admin:password@192.168.1.10/stream

  # Quality variants using FFmpeg
  camera1-360p: ffmpeg:camera1#video=h264#height=360
  camera1-720p: ffmpeg:camera1#video=h264#height=720
  camera1-1080p: ffmpeg:camera1#video=h264#height=1080

  # Multiple cameras
  camera2: rtsp://admin:password@192.168.1.11/stream
  camera2-360p: ffmpeg:camera2#video=h264#height=360
  camera2-720p: ffmpeg:camera2#video=h264#height=720
```

### Stream Name Resolution

The multistream module resolves stream names using this pattern:

| Request | Resolved Stream Name |
|---------|---------------------|
| `stream: "camera1", quality: ""` | `camera1` |
| `stream: "camera1", quality: "native"` | `camera1` |
| `stream: "camera1", quality: "360p"` | `camera1-360p` |
| `stream: "camera1", quality: "720p"` | `camera1-720p` |
| `stream: "camera1", quality: "1080p"` | `camera1-1080p` |

### Codec Considerations

**Important:** All streams used in the same multistream session should use compatible codecs:

- **Recommended:** H264 for maximum compatibility
- **Supported:** H265 (Safari only for WebRTC)
- **Avoid:** Mixing H264 and H265 in the same session

```yaml
streams:
  # Ensure all variants use the same codec (h264)
  camera1-360p: ffmpeg:camera1#video=h264#height=360
  camera1-720p: ffmpeg:camera1#video=h264#height=720
  camera1-1080p: ffmpeg:camera1#video=h264#height=1080
```

---

## WebSocket Protocol

### Message Format

**Important:** All messages use the go2rtc WebSocket format:

```json
{
  "type": "message_type",
  "value": { ...message data... }
}
```

The `type` field identifies the message type, and all other fields must be wrapped in the `value` object. This is the standard go2rtc WebSocket API format used by all modules.

### Connection Flow

```
┌──────────┐                                    ┌──────────┐
│  Client  │                                    │  Server  │
└────┬─────┘                                    └────┬─────┘
     │                                               │
     │  1. WebSocket Connect                         │
     │  ─────────────────────────────────────────────>
     │                                               │
     │  2. multistream/init                          │
     │  { slots: [...] }                             │
     │  ─────────────────────────────────────────────>
     │                                               │
     │  3. multistream/ready                         │
     │  { slots: N }                                 │
     │  <─────────────────────────────────────────────
     │                                               │
     │  4. Create RTCPeerConnection                  │
     │     Add N recvonly transceivers               │
     │     Create offer                              │
     │                                               │
     │  5. multistream/offer                         │
     │  { sdp: "..." }                               │
     │  ─────────────────────────────────────────────>
     │                                               │
     │  6. multistream/answer                        │
     │  { sdp: "...", slots: [...] }                 │
     │  <─────────────────────────────────────────────
     │                                               │
     │  7. ICE Candidate Exchange                    │
     │  <────────────────────────────────────────────>
     │                                               │
     │  8. WebRTC Connected - Video Streams Active   │
     │  ═══════════════════════════════════════════  │
     │                                               │
     │  9. multistream/switch (anytime)              │
     │  { slot: 0, stream: "camera2" }               │
     │  ─────────────────────────────────────────────>
     │                                               │
     │  10. multistream/status                       │
     │  { slot: 0, status: "active" }                │
     │  <─────────────────────────────────────────────
     │                                               │
```

### Message Reference

#### Client → Server Messages

**multistream/init**

Initialize a multistream session with requested slots:

```json
{
  "type": "multistream/init",
  "value": {
    "request_id": "uuid-123",
    "slots": [
      { "slot": 0, "stream": "camera1", "quality": "360p" },
      { "slot": 1, "stream": "camera2", "quality": "360p" },
      { "slot": 2, "stream": "camera3", "quality": "360p" }
    ]
  }
}
```

**multistream/offer**

Send WebRTC SDP offer after receiving `multistream/ready`:

```json
{
  "type": "multistream/offer",
  "value": {
    "request_id": "uuid-456",
    "sdp": "v=0\r\no=- 123456789 2 IN IP4 127.0.0.1\r\n..."
  }
}
```

**multistream/switch**

Switch a slot to a different stream (instant, no reconnection):

```json
{
  "type": "multistream/switch",
  "value": {
    "request_id": "uuid-789",
    "slot": 0,
    "stream": "camera4",
    "quality": "720p"
  }
}
```

**multistream/pause**

Pause a slot (stops transcoding for this consumer but keeps slot allocated). If other consumers are viewing the same stream, transcoding continues for them:

```json
{
  "type": "multistream/pause",
  "value": {
    "request_id": "uuid-890",
    "slot": 0
  }
}
```

**multistream/resume**

Resume a paused slot by rebinding it to a stream:

```json
{
  "type": "multistream/resume",
  "value": {
    "request_id": "uuid-901",
    "slot": 0,
    "stream": "camera1",
    "quality": "360p"
  }
}
```

**multistream/keyframe**

Request a keyframe (I-frame) for a specific slot or all slots. Useful for recovery after packet loss or when seeking. Use `slot: -1` to request keyframes for all slots:

```json
{
  "type": "multistream/keyframe",
  "value": {
    "request_id": "uuid-012",
    "slot": 0
  }
}
```

Response:

```json
{
  "type": "multistream/keyframe",
  "value": {
    "request_id": "uuid-012",
    "slot": 0,
    "status": "requested"
  }
}
```

> **Note**: Actual keyframe delivery depends on the producer type. RTSP sources may respond to keyframe requests, while FFmpeg exec sources may not.

**multistream/ice**

Send ICE candidate to server:

```json
{
  "type": "multistream/ice",
  "value": {
    "candidate": "candidate:1 1 UDP 2013266431 192.168.1.100 50000 typ host"
  }
}
```

**multistream/close**

Gracefully close the session:

```json
{
  "type": "multistream/close",
  "value": {}
}
```

#### Server → Client Messages

**multistream/ready**

Session initialized, client should send offer:

```json
{
  "type": "multistream/ready",
  "value": {
    "request_id": "uuid-123",
    "slots": 3
  }
}
```

**multistream/answer**

WebRTC SDP answer with slot statuses:

```json
{
  "type": "multistream/answer",
  "value": {
    "request_id": "uuid-456",
    "sdp": "v=0\r\no=- 987654321 2 IN IP4 0.0.0.0\r\n...",
    "slots": [
      { "slot": 0, "stream": "camera1-360p", "status": "active" },
      { "slot": 1, "stream": "camera2-360p", "status": "active" },
      { "slot": 2, "stream": "camera3-360p", "status": "error", "error": "stream not found" }
    ]
  }
}
```

**multistream/status**

Slot status update (after switch or state change):

```json
{
  "type": "multistream/status",
  "value": {
    "slot": 0,
    "status": {
      "slot": 0,
      "stream": "camera4-720p",
      "status": "active"
    }
  }
}
```

**multistream/ice**

ICE candidate from server:

```json
{
  "type": "multistream/ice",
  "value": "candidate:1 1 UDP 2013266431 192.168.1.123 8555 typ host"
}
```

**multistream/disconnected**

Sent when the WebRTC connection is lost and the session is being cleaned up:

```json
{
  "type": "multistream/disconnected",
  "value": {
    "reason": "connection_lost"
  }
}
```

This message is sent when:
- The WebRTC connection enters `failed` state (ICE failure, immediate cleanup)
- The connection was `disconnected` for longer than the grace period (10 seconds)

The client should handle this by initiating a full reconnection if needed.

### Status Values

| Status | Description |
|--------|-------------|
| `pending` | Slot created, not yet bound to a stream |
| `active` | Stream is connected and video is flowing |
| `buffering` | Switching streams, waiting for video data |
| `paused` | Slot paused via `multistream/pause` (no transcoding for this consumer) |
| `offline` | Stream source is offline or disconnected |
| `error` | Error occurred (check `error` field for details) |
| `inactive` | Slot unbound from stream |

---

## Frontend Integration

### TypeScript Client

The `www/multistream-client.ts` file provides a complete TypeScript client:

```typescript
import { MultiStreamClient, SlotConfig, SlotStatus } from './multistream-client';

// Create client
const client = new MultiStreamClient('https://device.example.com', {
  debug: true,  // Enable console logging
  connectionTimeout: 10000,  // 10 second timeout
  pcConfig: {
    iceServers: [{ urls: 'stun:stun.l.google.com:19302' }]
  }
});

// Define slots
const slots: SlotConfig[] = [
  { slot: 0, stream: 'camera1', quality: '360p' },
  { slot: 1, stream: 'camera2', quality: '360p' },
  { slot: 2, stream: 'camera3', quality: '360p' },
];

// Status callback
const handleStatus = (slot: number, status: SlotStatus) => {
  console.log(`Slot ${slot}: ${status.status}`);
  if (status.error) {
    console.error(`Slot ${slot} error: ${status.error}`);
  }
};

// Connect
const trackMap = await client.connect(slots, handleStatus);

// Attach tracks to video elements
trackMap.forEach((track, slotIndex) => {
  const video = document.getElementById(`video-${slotIndex}`) as HTMLVideoElement;
  video.srcObject = new MediaStream([track]);
  video.play().catch(() => {
    // Autoplay blocked, try muted
    video.muted = true;
    video.play();
  });
});

// Switch camera on slot 0 to camera4 at 1080p
await client.switchStream(0, 'camera4', '1080p');

// Check connection state
console.log('Connected:', client.isConnected);
console.log('WebSocket state:', client.wsState);
console.log('PeerConnection state:', client.pcState);

// Disconnect when done
client.disconnect();
```

### React Integration

```tsx
import { useEffect, useRef, useState, useCallback } from 'react';
import { MultiStreamClient, SlotConfig, SlotStatus } from './multistream-client';

interface Camera {
  id: string;
  name: string;
}

interface CameraGridProps {
  deviceUrl: string;
  cameras: Camera[];
  defaultQuality?: string;
}

export function CameraGrid({ 
  deviceUrl, 
  cameras, 
  defaultQuality = '360p' 
}: CameraGridProps) {
  const clientRef = useRef<MultiStreamClient | null>(null);
  const videoRefs = useRef<Map<number, HTMLVideoElement>>(new Map());
  const [statuses, setStatuses] = useState<Map<number, SlotStatus>>(new Map());
  const [connected, setConnected] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Initialize connection
  useEffect(() => {
    const client = new MultiStreamClient(deviceUrl, { debug: true });
    clientRef.current = client;

    const slots: SlotConfig[] = cameras.map((cam, i) => ({
      slot: i,
      stream: cam.id,
      quality: defaultQuality,
    }));

    const handleStatus = (slot: number, status: SlotStatus) => {
      setStatuses(prev => new Map(prev).set(slot, status));
    };

    client.connect(slots, handleStatus)
      .then(trackMap => {
        setConnected(true);
        setError(null);
        
        // Attach tracks to video elements
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
      })
      .catch(err => {
        setError(err.message);
        setConnected(false);
      });

    return () => {
      client.disconnect();
      setConnected(false);
    };
  }, [deviceUrl, cameras, defaultQuality]);

  // Handle camera click - switch to high quality
  const handleCameraClick = useCallback(async (slot: number, cameraId: string) => {
    if (!clientRef.current) return;
    
    try {
      await clientRef.current.switchStream(slot, cameraId, '1080p');
    } catch (err) {
      console.error('Switch failed:', err);
    }
  }, []);

  // Handle double-click - switch back to low quality
  const handleDoubleClick = useCallback(async (slot: number, cameraId: string) => {
    if (!clientRef.current) return;
    
    try {
      await clientRef.current.switchStream(slot, cameraId, defaultQuality);
    } catch (err) {
      console.error('Switch failed:', err);
    }
  }, [defaultQuality]);

  if (error) {
    return <div className="error">Connection error: {error}</div>;
  }

  return (
    <div className="camera-grid">
      {cameras.map((cam, slot) => (
        <div 
          key={cam.id} 
          className="camera-cell"
          onClick={() => handleCameraClick(slot, cam.id)}
          onDoubleClick={() => handleDoubleClick(slot, cam.id)}
        >
          <video
            ref={el => el && videoRefs.current.set(slot, el)}
            autoPlay
            playsInline
            muted
          />
          <div className="camera-overlay">
            <span className="camera-name">{cam.name}</span>
            <span className={`camera-status status-${statuses.get(slot)?.status || 'pending'}`}>
              {statuses.get(slot)?.status || 'connecting'}
            </span>
          </div>
        </div>
      ))}
    </div>
  );
}
```

### Vue Integration

```vue
<template>
  <div class="camera-grid">
    <div
      v-for="(camera, index) in cameras"
      :key="camera.id"
      class="camera-cell"
      @click="switchToHighQuality(index, camera.id)"
    >
      <video
        :ref="el => setVideoRef(index, el)"
        autoplay
        playsinline
        muted
      />
      <div class="camera-overlay">
        <span class="camera-name">{{ camera.name }}</span>
        <span :class="['camera-status', `status-${getStatus(index)}`]">
          {{ getStatus(index) }}
        </span>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted, watch } from 'vue';
import { MultiStreamClient, SlotConfig, SlotStatus } from './multistream-client';

const props = defineProps<{
  deviceUrl: string;
  cameras: Array<{ id: string; name: string }>;
  defaultQuality?: string;
}>();

const client = ref<MultiStreamClient | null>(null);
const videoRefs = ref<Map<number, HTMLVideoElement>>(new Map());
const statuses = ref<Map<number, SlotStatus>>(new Map());

const setVideoRef = (index: number, el: HTMLVideoElement | null) => {
  if (el) {
    videoRefs.value.set(index, el);
  }
};

const getStatus = (index: number): string => {
  return statuses.value.get(index)?.status || 'pending';
};

const switchToHighQuality = async (slot: number, cameraId: string) => {
  if (client.value) {
    await client.value.switchStream(slot, cameraId, '1080p');
  }
};

onMounted(async () => {
  const msClient = new MultiStreamClient(props.deviceUrl);
  client.value = msClient;

  const slots: SlotConfig[] = props.cameras.map((cam, i) => ({
    slot: i,
    stream: cam.id,
    quality: props.defaultQuality || '360p',
  }));

  const handleStatus = (slot: number, status: SlotStatus) => {
    statuses.value = new Map(statuses.value).set(slot, status);
  };

  const trackMap = await msClient.connect(slots, handleStatus);

  trackMap.forEach((track, slot) => {
    const video = videoRefs.value.get(slot);
    if (video) {
      video.srcObject = new MediaStream([track]);
      video.play().catch(() => {
        video.muted = true;
        video.play();
      });
    }
  });
});

onUnmounted(() => {
  client.value?.disconnect();
});
</script>
```

### Vanilla JavaScript

```html
<!DOCTYPE html>
<html>
<head>
  <title>Multistream Demo</title>
  <style>
    .camera-grid {
      display: grid;
      grid-template-columns: repeat(3, 1fr);
      gap: 8px;
    }
    .camera-cell video {
      width: 100%;
      background: #000;
    }
  </style>
</head>
<body>
  <div class="camera-grid" id="grid"></div>

  <script type="module">
    import { MultiStreamClient } from './multistream-client.js';

    const deviceUrl = 'https://device.example.com';
    const cameras = [
      { id: 'camera1', name: 'Front Door' },
      { id: 'camera2', name: 'Back Yard' },
      { id: 'camera3', name: 'Garage' },
    ];

    const grid = document.getElementById('grid');
    const client = new MultiStreamClient(deviceUrl);

    // Create video elements
    cameras.forEach((cam, i) => {
      const cell = document.createElement('div');
      cell.className = 'camera-cell';
      cell.innerHTML = `
        <video id="video-${i}" autoplay playsinline muted></video>
        <div>${cam.name}</div>
      `;
      cell.onclick = () => client.switchStream(i, cam.id, '1080p');
      grid.appendChild(cell);
    });

    // Connect
    const slots = cameras.map((cam, i) => ({
      slot: i,
      stream: cam.id,
      quality: '360p'
    }));

    client.connect(slots, (slot, status) => {
      console.log(`Slot ${slot}: ${status.status}`);
    }).then(trackMap => {
      trackMap.forEach((track, slot) => {
        const video = document.getElementById(`video-${slot}`);
        video.srcObject = new MediaStream([track]);
        video.play();
      });
    });
  </script>
</body>
</html>
```

---

## Migration from Iframes

### Before (Iframe Approach)

```tsx
// Each camera = separate iframe = separate WebRTC connection
function CameraView({ device, cameras }) {
  return (
    <div className="camera-grid">
      {cameras.map(cam => (
        <iframe
          key={cam.id}
          src={`${device.url}/stream.html?src=${cam.id}`}
          allow="autoplay"
        />
      ))}
    </div>
  );
}
```

**Problems:**
- 6 cameras = 6 WebRTC connections = 6x ICE negotiation
- Switching cameras requires: destroy iframe → create iframe → load HTML → negotiate WebRTC → buffer video → play
- 2-5 second delay for each switch
- High memory usage (6 iframe DOMs)
- High device CPU (maintaining 6 connections)

### After (Multistream Approach)

```tsx
// Single connection for all cameras
function CameraView({ device, cameras }) {
  return (
    <CameraGrid
      deviceUrl={device.url}
      cameras={cameras}
      defaultQuality="360p"
    />
  );
}
```

**Benefits:**
- 6 cameras = 1 WebRTC connection = 1 ICE negotiation
- Switching cameras: send message → receive new video → done
- <300ms delay for switching
- ~40% less memory
- ~50% less device CPU

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
    <div className="camera-grid">
      {cameras.map(cam => (
        <iframe
          key={cam.id}
          src={`${device.url}/stream.html?src=${cam.id}`}
          allow="autoplay"
        />
      ))}
    </div>
  );
}
```

---

## Performance Comparison

| Metric | Iframe Approach | Multistream | Improvement |
|--------|-----------------|-------------|-------------|
| Initial connection (6 cameras) | 6-12 seconds | <2 seconds | **5-6x faster** |
| Camera switch time | 2-5 seconds | <300ms | **10x faster** |
| WebRTC connections | 6 | 1 | **6x fewer** |
| ICE negotiations | 6 | 1 | **6x fewer** |
| Device CPU usage | High | ~50% less | **2x better** |
| Browser memory | High (6 iframes) | ~40% less | **1.7x better** |
| Network overhead | 6 DTLS handshakes | 1 DTLS handshake | **6x less** |

---

## Automatic Connection Monitoring

The multistream module automatically monitors the WebRTC connection state and cleans up resources when the connection is lost. This prevents FFmpeg/transcoding processes from running indefinitely when a client disconnects unexpectedly (e.g., browser tab closed, network failure).

### How It Works

```
┌─────────────────────────────────────────────────────────────────────────┐
│  WebRTC Connection State Machine                                        │
│                                                                         │
│  ┌──────────┐                                                          │
│  │   new    │                                                          │
│  └────┬─────┘                                                          │
│       │                                                                 │
│       ▼                                                                 │
│  ┌──────────┐      ICE failed      ┌──────────┐                       │
│  │connecting├─────────────────────►│  failed  │──► Immediate cleanup   │
│  └────┬─────┘                      └──────────┘                        │
│       │                                                                 │
│       │ ICE success                                                     │
│       ▼                                                                 │
│  ┌──────────┐    network glitch    ┌─────────────┐                    │
│  │connected ├─────────────────────►│disconnected │                    │
│  └────▲─────┘                      └──────┬──────┘                    │
│       │                                   │                            │
│       │    recovery                       │ 10s grace period           │
│       └───────────────────────────────────┤                            │
│                                           │ expired                    │
│                                           ▼                            │
│                                    Session cleanup:                    │
│                                    - Unbind all slots                  │
│                                    - Stop transcoding                  │
│                                    - Release resources                 │
└─────────────────────────────────────────────────────────────────────────┘
```

### Connection States

| State | Description | Action |
|-------|-------------|--------|
| `connected` | Connection healthy | Cancel any pending cleanup timer |
| `disconnected` | Connection temporarily lost (network glitch) | Start 10-second grace period |
| `failed` | Connection unrecoverable (ICE failed) | Immediate cleanup |
| `closed` | Connection explicitly closed | Cleanup if not already done |

### Grace Period

When the connection enters the `disconnected` state, a 10-second grace period starts. This allows time for the connection to recover from brief network glitches without unnecessarily stopping transcoding. If the connection recovers to `connected` within this period, the cleanup timer is cancelled.

### Client Notification

When the connection is lost and cleanup occurs, the server sends a notification to the client (if the WebSocket is still open):

```json
{
  "type": "multistream/disconnected",
  "value": {
    "reason": "connection_lost"
  }
}
```

### TypeScript Client Handling

```typescript
interface DisconnectedMessage {
  type: 'multistream/disconnected';
  value: {
    reason: string;
  };
}

// Handle disconnection notification
ws.onmessage = (event) => {
  const msg = JSON.parse(event.data);
  if (msg.type === 'multistream/disconnected') {
    console.log('Connection lost:', msg.value.reason);
    // Handle reconnection logic
    handleReconnect();
  }
};
```

### Benefits

- **Automatic Resource Cleanup**: FFmpeg processes are stopped when clients disconnect
- **Network Resilience**: Brief network glitches don't cause unnecessary restarts
- **No Manual Monitoring**: Server handles all connection state tracking
- **Reduced Server Load**: Orphaned transcoding processes are cleaned up automatically

---

## Error Handling

### Common Errors

| Error | Cause | Solution |
|-------|-------|----------|
| `stream not found` | Stream name doesn't exist in go2rtc config | Verify stream is configured in `go2rtc.yaml` |
| `too many slots requested` | Requested more slots than `max_slots` | Increase `max_slots` in config or request fewer slots |
| `session not initialized` | Sent offer before init | Ensure you wait for `multistream/ready` before sending offer |
| `no active session` | Tried to switch on closed session | Check connection state before switching |
| `invalid slot index` | Slot index doesn't exist | Only use slot indices that were in the init request |

### Client-Side Error Handling

```typescript
try {
  const trackMap = await client.connect(slots, handleStatus);
} catch (error) {
  if (error.message.includes('timeout')) {
    // Connection timeout - try reconnecting
    await client.disconnect();
    await client.connect(slots, handleStatus);
  } else if (error.message.includes('WebSocket')) {
    // WebSocket error - check network
    showNetworkError();
  } else {
    // Other error
    console.error('Connection failed:', error);
  }
}
```

---

## Troubleshooting

### Stream Not Found

**Symptom:** Slot status shows "error" with "stream not found"

**Solution:** Ensure the stream is configured in go2rtc:

```yaml
streams:
  camera1: rtsp://...
  camera1-360p: ffmpeg:camera1#video=h264#height=360
```

### ICE Connection Failed

**Symptom:** WebRTC connection fails after signaling

**Solution:** Check network/firewall settings:

1. Ensure STUN server is accessible: `stun:stun.l.google.com:19302`
2. Check if UDP port 8555 is open
3. Consider using TURN server for restrictive networks

### Video Not Playing

**Symptom:** Track attached but video element is black

**Solution:**

```typescript
video.play().catch(err => {
  // Autoplay blocked - try muted
  video.muted = true;
  video.play();
});
```

### Codec Mismatch

**Symptom:** Video plays on some browsers but not others

**Solution:** Ensure all quality variants use the same codec:

```yaml
streams:
  camera1-360p: ffmpeg:camera1#video=h264#height=360  # H264
  camera1-720p: ffmpeg:camera1#video=h264#height=720  # H264 (same!)
```

---

## API Reference

### MultiStreamClient

```typescript
class MultiStreamClient {
  constructor(baseUrl: string, options?: MultiStreamClientOptions);
  
  // Connect to server and establish WebRTC connection
  connect(
    slots: SlotConfig[],
    onStatus?: StatusCallback
  ): Promise<Map<number, MediaStreamTrack>>;
  
  // Switch a slot to a different stream (instant!)
  switchStream(
    slot: number,
    stream: string,
    quality?: string
  ): Promise<SlotStatus>;
  
  // Connection state
  readonly isConnected: boolean;
  readonly wsState: number;
  readonly pcState: RTCPeerConnectionState | null;
  
  // Disconnect and clean up
  disconnect(): void;
}
```

### Types

```typescript
interface SlotConfig {
  slot: number;      // Slot index (0-based)
  stream: string;    // Stream name
  quality?: string;  // Quality suffix (e.g., "360p")
}

interface SlotStatus {
  slot: number;
  stream: string;
  quality?: string;
  actualQuality?: string;
  status: 'pending' | 'active' | 'buffering' | 'paused' | 'offline' | 'error' | 'inactive';
  error?: string;
}

interface MultiStreamClientOptions {
  pcConfig?: RTCConfiguration;
  connectionTimeout?: number;  // Default: 10000ms
  debug?: boolean;             // Enable console logging
}

type StatusCallback = (slot: number, status: SlotStatus) => void;
```

---

## Limitations

1. **Codec Consistency:** All streams in a session should use compatible codecs (H264 recommended)

2. **Maximum Slots:** Limited by `max_slots` config (default: 9)

3. **Audio:** Audio is per-track (no mixing). In grid views, audio is typically muted.

4. **Browser Support:** Requires WebRTC support:
   - Chrome 60+
   - Firefox 55+
   - Safari 11+
   - Edge 79+

5. **Network:** Requires network path for WebRTC (may need TURN server for restrictive networks)

6. **Session Persistence:** Session state is lost on WebSocket disconnect. Client must reconnect and reinitialize.
