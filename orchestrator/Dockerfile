FROM python:3.11-slim
WORKDIR /app
COPY requirements.txt .
RUN pip install -U pip && pip install -r requirements.txt
COPY . .
EXPOSE 8080
# ⬇️ IMPORTANT: flat module (no package prefix)
CMD ["uvicorn", "app:app", "--host", "0.0.0.0", "--port", "8080"]
