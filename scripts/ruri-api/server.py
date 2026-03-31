#!/usr/bin/env python3
import json
import os
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import torch
from sentence_transformers import SentenceTransformer


MODEL_ID = os.environ.get("RURI_MODEL", "cl-nagoya/ruri-v3-130m")
HOST = os.environ.get("RURI_HOST", "127.0.0.1")
PORT = int(os.environ.get("RURI_PORT", "8000"))
DEVICE = os.environ.get("RURI_DEVICE") or ("cuda" if torch.cuda.is_available() else "cpu")


def prefix_for(input_type: str) -> str:
    if input_type == "query":
        return "検索クエリ: "
    if input_type == "document":
        return "検索文書: "
    return ""


print(f"loading model={MODEL_ID} device={DEVICE}", flush=True)
MODEL = SentenceTransformer(MODEL_ID, device=DEVICE)
EMBED_DIM = int(MODEL.get_sentence_embedding_dimension())
print(f"ready model={MODEL_ID} dimension={EMBED_DIM}", flush=True)


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/healthz":
            self.respond(200, {"ok": True, "model": MODEL_ID, "dimension": EMBED_DIM, "device": DEVICE})
            return
        self.respond(404, {"error": "not found"})

    def do_POST(self):
        if self.path != "/embed":
            self.respond(404, {"error": "not found"})
            return
        try:
            length = int(self.headers.get("Content-Length", "0"))
            raw = self.rfile.read(length)
            payload = json.loads(raw.decode("utf-8"))
            text = str(payload.get("input", "")).strip()
            input_type = str(payload.get("input_type", "generic")).strip() or "generic"
            if not text:
                self.respond(400, {"error": "input is required"})
                return
            prefixed = prefix_for(input_type) + text
            vector = MODEL.encode([prefixed], normalize_embeddings=True)[0]
            self.respond(
                200,
                {
                    "embedding": vector.tolist(),
                    "model": MODEL_ID,
                    "dimension": EMBED_DIM,
                    "input_type": input_type,
                },
            )
        except Exception as exc:
            self.respond(500, {"error": str(exc)})

    def log_message(self, fmt, *args):
        return

    def respond(self, status, payload):
        raw = json.dumps(payload).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)


def main():
    server = ThreadingHTTPServer((HOST, PORT), Handler)
    print(f"listening http://{HOST}:{PORT}", flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
