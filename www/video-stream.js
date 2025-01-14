import { VideoRTC } from './video-rtc.js';
import { FacialTracking } from './video-facial-tracking.js';

class VideoStream extends VideoRTC {
    set divMode(value) {
        window.parent.postMessage(JSON.stringify({ "VIDEO_MODE": value }), "*");
    }

    set divError(value) {
        // if (state !== "loading") return;
        window.parent.postMessage(JSON.stringify({ "VIDEO_ERROR": value }), "*");
    }

    /**
     * Custom GUI
     */
    async oninit() {
        console.debug('stream.oninit');
        super.oninit();

        this.innerHTML = `
        <style>
        video-stream {
            position: relative;
        }
        .info {
            position: absolute;
            top: 0;
            left: 0;
            right: 0;
            padding: 12px;
            color: white;
            display: none;
            justify-content: space-between;
            pointer-events: none;
        }
        </style>
        <div class="info">
            <div class="status"></div>
            <div class="mode"></div>
        </div>
        `;

        const info = this.querySelector('.info');
        this.insertBefore(this.video, info);

        // Add monitoring logic for video playback state
        this.monitorVideoPlayback();

        this.facialTracking = new FacialTracking(this.video);

        await this.facialTracking.loadFaceApi();
        window.parent.postMessage('FACE_TRACKING_READY', '*');

        window.addEventListener('message', (event) => {
            const { action } = event.data || {};
            if (action === 'start_face_tracking') this.facialTracking.startFaceTracking();
            else if (action === 'stop_face_tracking') this.facialTracking.stopFaceTracking();
        });

    }

    monitorVideoPlayback() {
        const videoElement = this.video;
        if (!videoElement) return;

        let hasStartedPlaying = false;
        let playbackTimeout;

        const videoSrcs = window.go2rtc_streams?.join(',') || 'unknown';

        // Timeout to check if playback hasn't started within 15 seconds
        const notStartedTimeout = setTimeout(() => {
            if (!hasStartedPlaying) {
                window.parent.postMessage(`PLAYBACK_NOT_STARTED:${videoSrcs}`, "*");
            }
        }, 15000); // 15 seconds timeout

        // Monitor playback progress to detect if the video hangs
        let lastTime = videoElement.currentTime;

        const checkIfVideoIsHung = () => {
            if (hasStartedPlaying) {
                if (videoElement.currentTime === lastTime && !videoElement.paused && videoElement.readyState >= 2) {
                    console.warn('The video appears to be hung (no progress in playback).');
                    window.parent.postMessage(`PLAYBACK_HUNG:${videoSrcs}`, "*");
                } else {
                    console.log('The video seems to be playing normally.');
                    lastTime = videoElement.currentTime; // Update the last known time
                }
            }
        };

        // Listen for when playback starts
        videoElement.addEventListener('playing', () => {
            hasStartedPlaying = true;
            clearTimeout(notStartedTimeout); // Clear the not started timeout if playback begins
            console.log('Video playback has started.');
        });

        // Periodically check for playback hangs (adjust interval as needed)
        playbackTimeout = setInterval(checkIfVideoIsHung, 10000); // 10 seconds interval

        // Clear intervals and timeouts on video end
        videoElement.addEventListener('ended', () => {
            clearTimeout(playbackTimeout);
            clearTimeout(notStartedTimeout);
        });
    }

    onconnect() {
        console.debug('stream.onconnect');
        const result = super.onconnect();
        if (result) this.divMode = 'loading';
        return result;
    }

    ondisconnect() {
        console.debug('stream.ondisconnect');
        super.ondisconnect();
    }

    onopen() {
        console.debug('stream.onopen');
        const result = super.onopen();

        this.onmessage['stream'] = msg => {
            console.debug('stream.onmessage', msg);
            switch (msg.type) {
                case 'error':
                    this.divError = msg.value;
                    break;
                case 'mse':
                case 'hls':
                case 'mp4':
                case 'mjpeg':
                    this.divMode = msg.type.toUpperCase();
                    break;
            }
        };

        return result;
    }

    onclose() {
        console.debug('stream.onclose');
        return super.onclose();
    }

    onpcvideo(ev) {
        console.debug('stream.onpcvideo');
        super.onpcvideo(ev);

        // Include the list of streams in the STARTING_VIDEO message
        const videoSrcs = window.go2rtc_streams?.join(',') || 'unknown';
        const message = `STARTING_VIDEO:${videoSrcs}`;
        window.parent.postMessage(message, "*");

        if (this.pcState !== WebSocket.CLOSED) {
            this.divMode = 'RTC';
        }
    }
}

customElements.define('video-stream', VideoStream);