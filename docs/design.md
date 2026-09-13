# Making DSKY feel like something

## What is actually wrong

The app is not badly styled. It is unstyled: one CSS transition, one keyframe,
no SVG, no elevation, the system sans at one weight, and a dark grey that was
chosen by not choosing.

That is a good position to start from — there is nothing to undo — but it
misses what this tool has and almost nothing else does. It writes to physical
hardware. A stick gets plugged in. Bytes move at eighty megabytes a second. A
machine is about to be reimaged and then verified byte for byte against a hash.

All of that currently renders as grey rows and a five-pixel bar.

The word for it is not ugly. It is **inert**. Most of the work below is giving
the interesting moments their due rather than decorating the dull ones.

## The brand gap

uplinkresearch.com is distinctive: mono type, cyan and orange on near-black, a
CRT treatment. DSKY is generic dark-mode blue. Somebody arriving from the
site meets what looks like a different company's product.

Close the gap, but restrained. Scanlines, curvature and Press Start 2P are
landing-page devices — they work for eight seconds and are exhausting for
twenty minutes. What carries across is the type discipline and the palette, not
the effects.

## The three that matter most

### 1. The write is the centrepiece, so make it one

Flashing takes two to ten minutes. It is the whole point of the app and it is
currently a thin bar with a percentage.

It should show throughput, elapsed and remaining, bytes against total — and the
**verify pass as a visibly distinct second phase**. That phase already exists
in the engine: every write is read back and hashed. Watching it happen is the
most reassuring thing this tool can offer, and right now nobody can tell it
happened at all.

End on the hash. "Written and verified, byte for byte" is the best sentence
this app gets to say.

### 2. Give the operating systems faces

Twenty-seven entries in a dropdown with no marks. Logos are recognised before
they are read, and would turn the install door from a list into a choice.

Costs: a logo field in the catalogue, bundled SVGs, and a trademark check —
distro logos are marks with usage policies, and most permit use that refers to
the distribution, but that is worth confirming rather than assuming.

The dodge, if the licensing is tedious: every distribution has a signature
colour, and a consistent set of abstract marks gets most of the recognition
with none of the legal question.

### 3. Feel aware of the hardware

A stick appearing should arrive — animate in, be noticed. Unplugging should
register as an event rather than a list that silently differs.

This is the difference between a web page about disks and a tool connected to
the machine it is running on.

## The visual system

Typography is the cheapest large win:

- a humanist sans for prose
- **JetBrains Mono for every path, size, serial and hash** — functionally
  better, not merely thematic, and already the face the site uses
- tabular numerals anywhere a number changes in place

Then a real spacing scale rather than ad-hoc padding; depth from elevation
instead of a border around every box; cyan and orange as the accents in place
of the generic blue; and empty states that look designed rather than
apologetic.

## Satisfying is mostly not motion

The things that make a tool feel good are largely invisible:

- the window appearing quickly
- the window remembering its size
- Esc to go back, Enter to confirm
- focus rings that look deliberate rather than inherited
- the arm-to-confirm step showing progress toward the string it wants, instead
  of failing silently until it matches

One structural nit: the update banner is a permanent bar across the top of the
product. It should be quieter.

## What not to do

- scanlines or CRT curvature inside the app
- animating everything
- a splash screen
- sound, beyond perhaps one optional completion tone
- making the destructive confirmation prettier at the cost of legibility. That
  screen's only job is to be read.

## Order

1. Type and palette, because everything else is judged against them
2. The write-and-verify moment
3. Device presence
4. OS marks — the biggest content lift
5. Polish, last, because it is only worth doing once the shapes have settled

## Two caveats worth keeping in the file

Taste is the owner's, not the implementer's. What is written above is a
diagnosis of what is inert, which is a different and smaller claim than knowing
what is beautiful.

And none of this ordering survives contact with use. The screens somebody
actually dwells on are not reliably the ones that look worst in a screenshot.
