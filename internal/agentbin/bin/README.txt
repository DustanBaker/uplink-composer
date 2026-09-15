The first-boot agent is compiled into this directory by ./build-agent.sh and
embedded into DSKY, which stages it onto Windows media. The binaries are build
outputs and are not kept in git; a DSKY built without them has no agent, and
composes media with the generated scripts instead.
