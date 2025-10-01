from pydantic import BaseModel, Field, EmailStr
from typing import List, Optional, Dict

class VerifyRequest(BaseModel):
    emails: List[EmailStr]
    sender: Optional[EmailStr] = Field(default="noreply@example.com")
    job_id: Optional[str] = None

class VerifyTask(BaseModel):
    job_id: str
    domain: str
    emails: List[EmailStr]
    sender: EmailStr
    opts: Dict[str, str] = {}

class VerifyResult(BaseModel):
    job_id: str
    email: EmailStr
    status: str
    message: str = ""
    mx: str = ""
    domain: str = ""
