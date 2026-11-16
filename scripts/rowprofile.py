#!/usr/bin/env python3
"""Print the mean brightness of each row of a frame, to find where a panel actually is.

    python3 rowprofile.py /tmp/frame.png

WHY: `IsLinuxDesktop` looks at the lowest 4.5% of the frame for a flat band. That is where a Linux
panel sits. Android's Taskbar is TALLER and sits a little higher, so the band it looks at may hold
nothing but the gesture bar - and a detector that checks the wrong rows reports "no desktop" on a
frame that is one.
"""
import sys

from PIL import Image


def main(path):
    im = Image.open(path).convert("RGB")
    w, h = im.size
    px = im.load()
    print(f"{w}x{h}")
    print("  row   mean   spread")
    for y in range(0, h, max(1, h // 60)):
        vals = [sum(px[x, y]) / 3.0 for x in range(0, w, max(1, w // 200))]
        mean = sum(vals) / len(vals)
        spread = max(vals) - min(vals)
        pct = y / h * 100
        bar = "#" * int(mean / 8)
        print(f"  {y:4d}  {mean:6.1f}  {spread:6.1f}  {pct:5.1f}%  {bar}")


if __name__ == "__main__":
    main(sys.argv[1])
