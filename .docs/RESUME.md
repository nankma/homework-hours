# Resuming in a new session

1. Read, in order: `../README.md` → `STATUS.md` → the work list (local, see below) → the
   relevant part of `DESIGN.md`. `DECISIONS.md` explains why things are the way they are;
   don't re-open settled decisions unless the user does.
2. Working agreements with the user:
   - Talk designs through fully before building; the user decides. Don't push ahead on
     something not yet agreed.
   - Chat in Chinese; everything written to disk (code, comments, docs, commit messages) in
     English.
   - Confirm before risky or hard-to-reverse actions on the live network (cabling changes,
     enabling the DNS redirect at boot, network reloads).
3. Check the CM4 is as documented:
   ```sh
   ssh -i ~/.ssh/cm4pi_rsa root@192.168.0.2 '
     /etc/init.d/homework-hours status; /etc/init.d/adguardhome status
     nft list tables; bridge link; logread -e homework-hours | tail -5'
   ```
4. Build/deploy/test: `OPERATIONS.md`. Package builds: `../CLAUDE.md`.
5. When something changes, update `STATUS.md`, the work list (and `DECISIONS.md` for new
   decisions) in the same session.

## Machine-specific notes
Paths, users, local-only files (including the work list) and dev box quirks: `../CLAUDE.local.md`
when present.
