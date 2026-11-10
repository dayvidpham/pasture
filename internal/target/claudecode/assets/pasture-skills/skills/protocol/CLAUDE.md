# Agent directive

Run the epoch with [epoch](../epoch/SKILL.md). That skill owns detection, the tracker backend, and which role skill to load.

At session start, recover the live task state and select one tracker backend. Use the same backend for the epoch. Read the requirements and plan before implementation. Workers report evidence; the supervisor closes reviewed work.

Do not keep a second copy of the procedure in a project instruction file. Point the project at the generated epoch skill.

Role ownership is in [AGENTS.md](AGENTS.md). Phase pointers are in [PROCESS.md](PROCESS.md).
