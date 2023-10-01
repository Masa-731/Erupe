# Erupe Community Edition

This fork is based on SU9.1 beta, and has diva prayer gems/diva defense special guild hall enabled, and diva skill exchange rate modified.
To set prayer gems, make sure to run  `patch-schema\!DB_setup.bat` use `!song` and `!effect` commands in game chat (for details, see `server\channelserver\handlers_cast_binary.go`) and activate at the quest counter (blue girl) after reloading your land.

## Setup
- If you are only looking to install Erupe, please use [a pre-compiled binary](https://github.com/ZeruLight/Erupe/releases/latest).
- If you want to modify or compile Erupe yourself please read on.
### Requirements
- [Go](https://go.dev/dl/)
- [PostgreSQL](https://www.postgresql.org/download/)
### Installation
1. Bring up a fresh database by using the [backup file attached with the latest release](https://github.com/ZeruLight/Erupe/releases/latest/download/Erupe.sql).
2. Run each script under [patch-schema](./patch-schema) as they introduce newer schema.
3. Edit [config.json](./config.json) such that the database password matches your PostgreSQL setup.
4. Run `go build` or `go run .` to compile Erupe.
### Note
- You will need to acquire and install the client files and quest binaries separately.
# Resources
[Community FAQ Pastebin](https://pastebin.com/QqAwZSTC)

[Quests and Scenario Binary Files](https://github.com/xl3lackout/MHFZ-Quest-Files)
