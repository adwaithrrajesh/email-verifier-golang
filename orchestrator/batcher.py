import uuid
import json
from typing import List, Dict
from redis import Redis
from models import VerifyTask

STREAM_TASKS = "verify.tasks"

def group_by_domain(emails: List[str]) -> Dict[str, List[str]]:
    out: Dict[str, List[str]] = {}
    for e in emails:
        parts = e.split("@")
        if len(parts) == 2:
            out.setdefault(parts[1].lower(), []).append(e)
    return out

def enqueue_tasks(r: Redis, job_id: str, sender: str, emails: List[str], chunk_size: int = 40):
    ids = []
    for domain, addr_list in group_by_domain(emails).items():
        for i in range(0, len(addr_list), chunk_size):
            chunk = addr_list[i : i + chunk_size]
            # Build a flat dict of string values for XADD
            payload = {
                "job_id": job_id,
                "domain": domain,
                "sender": sender,
                # 👇 JSON-encode the list so Redis gets a string
                "emails": json.dumps(chunk),
            }
            ids.append(r.xadd(STREAM_TASKS, payload))
    return ids

def new_job_id(job_id: str | None = None) -> str:
    return job_id or uuid.uuid4().hex
