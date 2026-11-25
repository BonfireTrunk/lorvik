// SPDX-License-Identifier: Apache-2.0

export const limits = Object.freeze({
  pendingBytes: 256 * 1024,
  pendingLines: 4096,
  lineBytes: 64 * 1024,
  frameBytes: 512 * 1024,
  receiptBytes: 64 * 1024,
  receiptConnections: 8,
  sendIntervalMs: 2100,
  receiptTimeoutMs: 15000,
});

export function boundedLines(input, cap, onLine, onDrop = () => {}) {
  let partial = "";
  let bytes = 0;
  let discarded = false;
  input.setEncoding("utf8");
  input.on("data", (chunk) => {
    let start = 0;
    while (start < chunk.length) {
      const newline = chunk.indexOf("\n", start);
      const end = newline < 0 ? chunk.length : newline;
      if (!discarded) {
        const part = chunk.slice(start, end);
        bytes += Buffer.byteLength(part);
        if (bytes > cap) {
          partial = "";
          discarded = true;
          onDrop();
        } else {
          partial += part;
        }
      }
      if (newline < 0) break;
      if (!discarded) onLine(partial.endsWith("\r") ? partial.slice(0, -1) : partial);
      partial = "";
      bytes = 0;
      discarded = false;
      start = newline + 1;
    }
  });
  input.on("end", () => {
    if (!discarded && partial) onLine(partial.endsWith("\r") ? partial.slice(0, -1) : partial);
    partial = "";
  });
}

export class PendingBatch {
  lines = [];
  bytes = 0;
  dropped = 0;

  get hasData() { return this.lines.length > 0 || this.dropped > 0; }

  add(line) {
    const bytes = Buffer.byteLength(line) + 1;
    if (bytes > limits.lineBytes + 1) {
      this.dropped++;
      return;
    }
    this.lines.push(line);
    this.bytes += bytes;
    this.trim();
  }

  prepend(batch) {
    this.lines = batch.lines.concat(this.lines);
    this.bytes += batch.bytes;
    this.dropped += batch.dropped;
    this.trim();
  }

  trim() {
    while (this.bytes > limits.pendingBytes || this.lines.length > limits.pendingLines) {
      this.bytes -= Buffer.byteLength(this.lines.shift()) + 1;
      this.dropped++;
    }
  }

  take(makeFrame) {
    const lines = [];
    let bytes = 0;
    let frameBytes = Buffer.byteLength(JSON.stringify(makeFrame([], this.dropped)));
    if (frameBytes > limits.frameBytes) throw new Error("Qwen frame metadata exceeds the adapter byte cap");
    while (this.lines.length) {
      const line = this.lines[0];
      const extra = Buffer.byteLength(JSON.stringify(line)) - 2 + (lines.length ? 2 : 0);
      if (frameBytes + extra > limits.frameBytes) {
        if (lines.length) break;
        this.bytes -= Buffer.byteLength(this.lines.shift()) + 1;
        this.dropped++;
        frameBytes = Buffer.byteLength(JSON.stringify(makeFrame([], this.dropped)));
        continue;
      }
      lines.push(this.lines.shift());
      bytes += Buffer.byteLength(line) + 1;
      this.bytes -= Buffer.byteLength(line) + 1;
      frameBytes += extra;
    }
    const batch = { lines, bytes, dropped: this.dropped };
    this.dropped = 0;
    const frame = makeFrame(batch.lines, batch.dropped);
    if (Buffer.byteLength(JSON.stringify(frame)) > limits.frameBytes) throw new Error("Qwen frame exceeds the adapter byte cap");
    return { batch, frame };
  }
}
