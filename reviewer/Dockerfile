FROM python:3.7-slim

WORKDIR /app
COPY requirements.txt /app
RUN pip install -r requirements.txt

COPY . /app
ENTRYPOINT [ "python", "-u", "/app/reviewer.py" ]