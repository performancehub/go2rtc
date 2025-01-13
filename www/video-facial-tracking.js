export class FacialTracking {
    constructor(videoElement) {
        if (!videoElement) {
            throw new Error('Video element is required for FacialTracking.');
        }

        this.videoElement = videoElement;
        this.canvas = document.createElement('canvas');
        this.tracker = new (class CentroidTracker {
            constructor(maxDisappeared = 10) {
                this.nextObjectID = 0;
                this.objects = {};
                this.disappeared = {};
                this.boundings = {};
                this.maxDisappeared = maxDisappeared;
            }

            register(centroid, rect) {
                this.objects[this.nextObjectID] = centroid;
                this.boundings[this.nextObjectID] = rect;
                this.disappeared[this.nextObjectID] = 0;
                this.nextObjectID++;
            }

            deregister(objectID) {
                delete this.objects[objectID];
                delete this.disappeared[objectID];
                delete this.boundings[objectID];
            }

            update(rects) {
                if (rects.length === 0) {
                    Object.keys(this.disappeared).forEach((objectID) => {
                        this.disappeared[objectID]++;
                        if (this.disappeared[objectID] > this.maxDisappeared) {
                            this.deregister(objectID);
                        }
                    });
                    return this.objects;
                }

                const inputCentroids = rects.map((rect) => {
                    const x = rect.x + rect.width / 2;
                    const y = rect.y + rect.height / 2;
                    return [x, y];
                });

                if (Object.keys(this.objects).length === 0) {
                    inputCentroids.forEach((centroid, i) => this.register(centroid, rects[i]));
                } else {
                    const objectIDs = Object.keys(this.objects);
                    const objectCentroids = Object.values(this.objects);

                    const distances = inputCentroids.map((inputCentroid) =>
                        objectCentroids.map((objectCentroid) =>
                            Math.sqrt((inputCentroid[0] - objectCentroid[0]) ** 2 + (inputCentroid[1] - objectCentroid[1]) ** 2)
                        )
                    );

                    const rows = distances.map((row, i) => ({
                        rowIndex: i,
                        minDistance: Math.min(...row),
                        colIndex: row.indexOf(Math.min(...row)),
                    }));
                    rows.sort((a, b) => a.minDistance - b.minDistance);

                    const usedRows = new Set();
                    const usedCols = new Set();

                    rows.forEach(({ rowIndex, colIndex }) => {
                        if (usedRows.has(rowIndex) || usedCols.has(colIndex)) return;

                        const objectID = objectIDs[colIndex];
                        this.objects[objectID] = inputCentroids[rowIndex];
                        this.boundings[objectID] = rects[rowIndex];
                        this.disappeared[objectID] = 0;

                        usedRows.add(rowIndex);
                        usedCols.add(colIndex);
                    });

                    objectIDs.forEach((objectID, i) => {
                        if (!usedCols.has(i)) {
                            this.disappeared[objectID]++;
                            if (this.disappeared[objectID] > this.maxDisappeared) {
                                this.deregister(objectID);
                            }
                        }
                    });

                    inputCentroids.forEach((centroid, i) => {
                        if (!usedRows.has(i)) {
                            this.register(centroid, rects[i]);
                        }
                    });
                }

                return this.objects;
            }
        })();

        this.detectionInterval = null;

        document.body.appendChild(this.canvas);
        this.canvas.style.position = 'absolute';
        this.canvas.style.zIndex = '1000';
        this.canvas.style.pointerEvents = 'none';

        this.updateCanvasSize();
        window.addEventListener('resize', () => this.updateCanvasSize());
    }

    async loadFaceApi() {
        return new Promise((resolve, reject) => {
            const script = document.createElement('script');
            script.src = 'https://content.gymsystems.co/face-api.js/dist/face-api.min.js';
            script.onload = async () => {
                try {
                    const modelURL = 'https://justadudewhohacks.github.io/face-api.js/models/';
                    await faceapi.nets.ssdMobilenetv1.loadFromUri(modelURL);
                    console.log('Face API loaded and model initialized.');
                    resolve();
                } catch (error) {
                    reject(error);
                }
            };
            script.onerror = (err) => reject(err);
            document.head.appendChild(script);
        });
    }

    updateCanvasSize() {
        const videoRect = this.videoElement.getBoundingClientRect();
        this.canvas.width = videoRect.width;
        this.canvas.height = videoRect.height;
        this.canvas.style.top = `${videoRect.top + window.scrollY}px`;
        this.canvas.style.left = `${videoRect.left + window.scrollX}px`;
    }

    async startFaceTracking() {
        if (this.detectionInterval) return;

        const displaySize = { width: this.canvas.width, height: this.canvas.height };
        faceapi.matchDimensions(this.canvas, displaySize);

        this.detectionInterval = setInterval(async () => {
            if (this.videoElement.paused || this.videoElement.ended) {
                clearInterval(this.detectionInterval);
                this.detectionInterval = null;
                return;
            }

            const detections = await faceapi
                .detectAllFaces(this.videoElement, new faceapi.SsdMobilenetv1Options({ minConfidence: 0.6 }))
                .then((res) => faceapi.resizeResults(res, displaySize));

            const rects = detections.map((d) => d.box);
            const trackedObjects = this.tracker.update(rects);

            const ctx = this.canvas.getContext('2d');
            ctx.clearRect(0, 0, this.canvas.width, this.canvas.height);

            Object.values(trackedObjects).forEach((centroid) => {
                const rect = Object.values(this.tracker.boundings).find(
                    (r) => r.x + r.width / 2 === centroid[0] && r.y + r.height / 2 === centroid[1]
                );
                if (!rect) return;

                const { x, y, width, height } = rect;

                ctx.strokeStyle = '#00BFFF';
                ctx.lineWidth = 3;

                ctx.strokeRect(x, y, width, height);
            });
        }, 100);
    }

    stopFaceTracking() {
        if (this.detectionInterval) {
            clearInterval(this.detectionInterval);
            this.detectionInterval = null;
            this.canvas.getContext('2d').clearRect(0, 0, this.canvas.width, this.canvas.height);
        }
    }
}