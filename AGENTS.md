# AGENTS.md

## Tool Installation
- `sudo dnf install <package>` is available for installing any needed tools

## Important Notes

### Docker Containers
- **NEVER use `docker stop $(docker ps -aq)`** - This will stop ALL containers including the opencode container you're running in
- Always use `docker compose` commands instead of individual container management to keep environment consistent
- When restarting services, specify individual services: `docker compose restart frontend` or `docker compose up -d --force-recreate frontend`
