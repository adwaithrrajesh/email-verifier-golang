import os
import json
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel
from redis import Redis

# 🔧 switched to absolute imports (no leading dot)
from models import VerifyRequest
from batcher import enqueue_tasks, new_job_id, STREAM_TASKS
from tier0 import quick_score

STREAM_RESULTS = "verify.results"
HASH_JOB_SUMMARY = "verify.job.summary"
REDIS_URL = os.getenv("REDIS_URL", "redis://localhost:6379/0")

app = FastAPI(title="Email Verify Orchestrator")

def rconn() -> Redis:
    return Redis.from_url(REDIS_URL, decode_responses=True)

@app.post("/verify/start")
def start_verify(req: VerifyRequest):
    if not req.emails:
        raise HTTPException(400, "emails required")

    r = rconn()
    job_id = new_job_id(req.job_id)

    # Tier-0 fast checks
    scores = {e: quick_score(e) for e in req.emails}
    r.hset(HASH_JOB_SUMMARY, job_id, json.dumps(scores))

    # Enqueue Tier-1 (SMTP) tasks
    enqueue_tasks(r, job_id, req.sender, list(map(str, req.emails)), chunk_size=40)

    return {"job_id": job_id, "queued": len(req.emails), "tier0_ready": True}

@app.get("/verify/{job_id}/tier0")
def get_tier0(job_id: str):
    r = rconn()
    raw = r.hget(HASH_JOB_SUMMARY, job_id)
    if not raw:
        raise HTTPException(404, "no such job")
    return json.loads(raw)

class Poll(BaseModel):
    last_id: str | None = None
    max: int = 200

@app.post("/verify/{job_id}/poll")
def poll(job_id: str, q: Poll):
    r = rconn()
    start = q.last_id or "0-0"
    items = r.xrange(STREAM_RESULTS, min=start, max="+", count=q.max)
    out, last = [], q.last_id
    for msg_id, fields in items:
        if fields.get("job_id") == job_id:
            out.append(fields)
        last = msg_id
    return {"last_id": last, "items": out}
