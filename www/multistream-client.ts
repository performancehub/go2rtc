/**
 * MultiStreamClient - Client library for go2rtc multistream WebRTC connections
 * 
 * This client enables a single WebRTC connection to view multiple camera streams
 * with instant switching capabilities (no reconnection required).
 * 
 * @example
 * ```typescript
 * const client = new MultiStreamClient('https://device.example.com');
 * 
 * const slots = [
 *   { slot: 0, stream: 'camera1', quality: '360p' },
 *   { slot: 1, stream: 'camera2', quality: '360p' },
 * ];
 * 
 * const trackMap = await client.connect(slots, (slot, status) => {
 *   console.log(`Slot ${slot} status: ${status.status}`);
 * });
 * 
 * // Attach tracks to video elements
 * trackMap.forEach((track, slot) => {
 *   const video = document.getElementById(`video-${slot}`);
 *   video.srcObject = new MediaStream([track]);
 *   video.play();
 * });
 * 
 * // Switch camera on slot 0 (instant - no reconnection!)
 * await client.switchStream(0, 'camera3', '720p');
 * ```
 */

// ============================================================================
// Type Definitions
// ============================================================================

export interface SlotConfig {
  /** Slot index (0-based) */
  slot: number;
  /** Stream name/ID */
  stream: string;
  /** Optional quality suffix (e.g., "360p", "720p", "1080p") */
  quality?: string;
}

export interface SlotStatus {
  /** Slot index */
  slot: number;
  /** Stream name */
  stream: string;
  /** Requested quality */
  quality?: string;
  /** Actual quality (may differ if requested unavailable) */
  actualQuality?: string;
  /** Current status */
  status: 'pending' | 'active' | 'buffering' | 'offline' | 'error' | 'inactive';
  /** Error message if status is "error" */
  error?: string;
}

export type StatusCallback = (slot: number, status: SlotStatus) => void;

export interface MultiStreamClientOptions {
  /** WebRTC peer connection configuration */
  pcConfig?: RTCConfiguration;
  /** Connection timeout in milliseconds (default: 10000) */
  connectionTimeout?: number;
  /** Enable debug logging */
  debug?: boolean;
}

// ============================================================================
// Internal Message Types
// ============================================================================

interface InitRequest {
  type: 'multistream/init';
  request_id: string;
  slots: SlotConfig[];
}

interface SwitchRequest {
  type: 'multistream/switch';
  request_id: string;
  slot: number;
  stream: string;
  quality?: string;
}

interface OfferMessage {
  type: 'multistream/offer';
  request_id: string;
  sdp: string;
}

interface ICEMessage {
  type: 'multistream/ice';
  candidate: string;
}

interface AnswerMessage {
  type: 'multistream/answer';
  request_id: string;
  sdp: string;
  slots: SlotStatus[];
}

interface StatusUpdate {
  type: 'multistream/status';
  slot: number;
  status: SlotStatus;
}

interface ReadyMessage {
  type: 'multistream/ready';
  request_id: string;
  slots: number;
}

type ServerMessage = AnswerMessage | StatusUpdate | ReadyMessage | { type: string; value?: string };

// ============================================================================
// MultiStreamClient Class
// ============================================================================

export class MultiStreamClient {
  private ws: WebSocket | null = null;
  private pc: RTCPeerConnection | null = null;
  private pendingCandidates: RTCIceCandidateInit[] = [];
  private onStatus: StatusCallback | null = null;
  private requestCallbacks = new Map<string, (data: any) => void>();
  private connected = false;
  private debug: boolean;
  private connectionTimeout: number;

  constructor(
    private baseUrl: string,
    private options: MultiStreamClientOptions = {}
  ) {
    this.debug = options.debug ?? false;
    this.connectionTimeout = options.connectionTimeout ?? 10000;
  }

  /**
   * Connect to the multistream server and establish WebRTC connection
   * 
   * @param slots - Array of slot configurations
   * @param onStatus - Optional callback for status updates
   * @returns Map of slot index to MediaStreamTrack
   */
  async connect(
    slots: SlotConfig[],
    onStatus?: StatusCallback
  ): Promise<Map<number, MediaStreamTrack>> {
    if (this.connected) {
      throw new Error('Already connected. Call disconnect() first.');
    }

    this.onStatus = onStatus ?? null;

    try {
      // 1. Create WebSocket connection
      await this.createWebSocket();

      // 2. Send init request and wait for ready
      const initId = this.generateId();
      await this.sendRequest<ReadyMessage>(
        {
          type: 'multistream/init',
          request_id: initId,
          slots: slots,
        },
        initId,
        'multistream/ready'
      );

      this.log('Session initialized, creating peer connection');

      // 3. Create peer connection
      this.createPeerConnection();

      // Add recvonly transceivers for each slot
      for (let i = 0; i < slots.length; i++) {
        this.pc!.addTransceiver('video', { direction: 'recvonly' });
      }

      // 4. Create and send offer
      const offer = await this.pc!.createOffer();
      await this.pc!.setLocalDescription(offer);

      const offerId = this.generateId();
      const answer = await this.sendRequest<AnswerMessage>(
        {
          type: 'multistream/offer',
          request_id: offerId,
          sdp: offer.sdp!,
        },
        offerId,
        'multistream/answer'
      );

      this.log('Received answer, setting remote description');

      // 5. Set remote answer
      await this.pc!.setRemoteDescription({ type: 'answer', sdp: answer.sdp });

      // Process any buffered ICE candidates
      for (const candidate of this.pendingCandidates) {
        await this.pc!.addIceCandidate(candidate);
      }
      this.pendingCandidates = [];

      // 6. Notify about initial slot statuses
      if (this.onStatus) {
        for (const status of answer.slots) {
          this.onStatus(status.slot, status);
        }
      }

      // 7. Build and return track map
      const trackMap = new Map<number, MediaStreamTrack>();
      const receivers = this.pc!.getReceivers();
      for (let i = 0; i < receivers.length; i++) {
        const track = receivers[i].track;
        if (track && track.kind === 'video') {
          trackMap.set(i, track);
        }
      }

      this.connected = true;
      this.log(`Connected with ${trackMap.size} video tracks`);

      return trackMap;
    } catch (error) {
      this.disconnect();
      throw error;
    }
  }

  /**
   * Switch a slot to a different stream (instant - no reconnection)
   * 
   * @param slot - Slot index to switch
   * @param stream - New stream name
   * @param quality - Optional quality suffix
   * @returns The new slot status
   */
  async switchStream(slot: number, stream: string, quality?: string): Promise<SlotStatus> {
    if (!this.connected) {
      throw new Error('Not connected. Call connect() first.');
    }

    const requestId = this.generateId();
    
    this.send({
      type: 'multistream/switch',
      request_id: requestId,
      slot,
      stream,
      quality,
    });

    // Wait for status update
    return new Promise((resolve, reject) => {
      const timeout = setTimeout(() => {
        reject(new Error('Switch timeout'));
      }, 5000);

      const originalCallback = this.onStatus;
      this.onStatus = (statusSlot, status) => {
        if (statusSlot === slot) {
          clearTimeout(timeout);
          this.onStatus = originalCallback;
          if (originalCallback) {
            originalCallback(statusSlot, status);
          }
          resolve(status);
        } else if (originalCallback) {
          originalCallback(statusSlot, status);
        }
      };
    });
  }

  /**
   * Get the current connection state
   */
  get isConnected(): boolean {
    return this.connected;
  }

  /**
   * Get the WebSocket ready state
   */
  get wsState(): number {
    return this.ws?.readyState ?? WebSocket.CLOSED;
  }

  /**
   * Get the peer connection state
   */
  get pcState(): RTCPeerConnectionState | null {
    return this.pc?.connectionState ?? null;
  }

  /**
   * Disconnect and clean up resources
   */
  disconnect(): void {
    this.connected = false;

    if (this.pc) {
      // Stop all tracks
      this.pc.getSenders().forEach(sender => {
        if (sender.track) {
          sender.track.stop();
        }
      });
      this.pc.close();
      this.pc = null;
    }

    if (this.ws) {
      // Send close message if connection is open
      if (this.ws.readyState === WebSocket.OPEN) {
        try {
          this.send({ type: 'multistream/close' });
        } catch (e) {
          // Ignore errors during close
        }
      }
      this.ws.close();
      this.ws = null;
    }

    this.pendingCandidates = [];
    this.requestCallbacks.clear();
    this.onStatus = null;

    this.log('Disconnected');
  }

  // ==========================================================================
  // Private Methods
  // ==========================================================================

  private async createWebSocket(): Promise<void> {
    const wsUrl = this.baseUrl.replace(/^http/, 'ws') + '/api/ws';
    this.log(`Connecting to WebSocket: ${wsUrl}`);

    return new Promise((resolve, reject) => {
      const timeout = setTimeout(() => {
        reject(new Error('WebSocket connection timeout'));
      }, this.connectionTimeout);

      this.ws = new WebSocket(wsUrl);

      this.ws.onopen = () => {
        clearTimeout(timeout);
        this.log('WebSocket connected');
        resolve();
      };

      this.ws.onerror = (event) => {
        clearTimeout(timeout);
        this.log('WebSocket error', event);
        reject(new Error('WebSocket connection failed'));
      };

      this.ws.onclose = (event) => {
        this.log(`WebSocket closed: code=${event.code}, reason=${event.reason}`);
        if (this.connected) {
          this.connected = false;
          // Could add auto-reconnect logic here
        }
      };

      this.ws.onmessage = (event) => {
        this.handleMessage(event.data);
      };
    });
  }

  private createPeerConnection(): void {
    const config = this.options.pcConfig ?? {
      iceServers: [{ urls: 'stun:stun.l.google.com:19302' }],
    };

    this.pc = new RTCPeerConnection(config);

    this.pc.onicecandidate = (event) => {
      if (event.candidate) {
        this.log('Sending ICE candidate');
        this.send({
          type: 'multistream/ice',
          candidate: event.candidate.candidate,
        });
      }
    };

    this.pc.onconnectionstatechange = () => {
      this.log(`Peer connection state: ${this.pc?.connectionState}`);
      
      if (this.pc?.connectionState === 'failed' || this.pc?.connectionState === 'closed') {
        this.disconnect();
      }
    };

    this.pc.oniceconnectionstatechange = () => {
      this.log(`ICE connection state: ${this.pc?.iceConnectionState}`);
    };
  }

  private handleMessage(data: string): void {
    let msg: ServerMessage;
    try {
      msg = JSON.parse(data);
    } catch (e) {
      this.log('Failed to parse message', data);
      return;
    }

    this.log('Received message', msg.type);

    // Handle request responses
    if ('request_id' in msg && msg.request_id && this.requestCallbacks.has(msg.request_id)) {
      const callback = this.requestCallbacks.get(msg.request_id)!;
      this.requestCallbacks.delete(msg.request_id);
      callback(msg);
      return;
    }

    // Handle ICE candidates from server
    if (msg.type === 'multistream/ice' && 'value' in msg) {
      const candidate: RTCIceCandidateInit = {
        candidate: msg.value as string,
        sdpMid: '0',
      };

      if (this.pc?.remoteDescription) {
        this.pc.addIceCandidate(candidate).catch(e => {
          this.log('Failed to add ICE candidate', e);
        });
      } else {
        this.pendingCandidates.push(candidate);
      }
      return;
    }

    // Handle status updates
    if (msg.type === 'multistream/status' && 'status' in msg) {
      const update = msg as StatusUpdate;
      if (this.onStatus) {
        this.onStatus(update.slot, update.status);
      }
      return;
    }
  }

  private send(msg: object): void {
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(msg));
    } else {
      throw new Error('WebSocket not connected');
    }
  }

  private sendRequest<T>(msg: object, requestId: string, expectedType: string): Promise<T> {
    return new Promise((resolve, reject) => {
      const timeout = setTimeout(() => {
        this.requestCallbacks.delete(requestId);
        reject(new Error(`Request timeout waiting for ${expectedType}`));
      }, this.connectionTimeout);

      this.requestCallbacks.set(requestId, (response) => {
        clearTimeout(timeout);
        if (response.type === 'error') {
          reject(new Error(response.value || 'Unknown error'));
        } else {
          resolve(response as T);
        }
      });

      this.send(msg);
    });
  }

  private generateId(): string {
    return Math.random().toString(36).substring(2, 15) + 
           Math.random().toString(36).substring(2, 15);
  }

  private log(...args: any[]): void {
    if (this.debug) {
      console.log('[MultiStreamClient]', ...args);
    }
  }
}

// ============================================================================
// React Hook (Optional)
// ============================================================================

/**
 * React hook for using MultiStreamClient
 * 
 * @example
 * ```tsx
 * function CameraGrid({ deviceUrl, cameras }) {
 *   const { tracks, statuses, switchStream, error } = useMultiStream(
 *     deviceUrl,
 *     cameras.map((cam, i) => ({ slot: i, stream: cam.id, quality: '360p' }))
 *   );
 * 
 *   return (
 *     <div>
 *       {cameras.map((cam, i) => (
 *         <video
 *           key={cam.id}
 *           ref={el => el && tracks.get(i) && (el.srcObject = new MediaStream([tracks.get(i)!]))}
 *           autoPlay
 *           playsInline
 *           muted
 *         />
 *       ))}
 *     </div>
 *   );
 * }
 * ```
 */
// Uncomment and adjust if using React:
/*
import { useEffect, useRef, useState, useCallback } from 'react';

export function useMultiStream(
  baseUrl: string,
  slots: SlotConfig[],
  options?: MultiStreamClientOptions
) {
  const clientRef = useRef<MultiStreamClient | null>(null);
  const [tracks, setTracks] = useState<Map<number, MediaStreamTrack>>(new Map());
  const [statuses, setStatuses] = useState<Map<number, SlotStatus>>(new Map());
  const [error, setError] = useState<Error | null>(null);
  const [connected, setConnected] = useState(false);

  useEffect(() => {
    const client = new MultiStreamClient(baseUrl, options);
    clientRef.current = client;

    const handleStatus = (slot: number, status: SlotStatus) => {
      setStatuses(prev => new Map(prev).set(slot, status));
    };

    client.connect(slots, handleStatus)
      .then(trackMap => {
        setTracks(trackMap);
        setConnected(true);
      })
      .catch(err => {
        setError(err);
      });

    return () => {
      client.disconnect();
      setConnected(false);
    };
  }, [baseUrl, JSON.stringify(slots)]);

  const switchStream = useCallback(
    async (slot: number, stream: string, quality?: string) => {
      if (clientRef.current) {
        return clientRef.current.switchStream(slot, stream, quality);
      }
      throw new Error('Client not initialized');
    },
    []
  );

  return { tracks, statuses, switchStream, error, connected };
}
*/

export default MultiStreamClient;
