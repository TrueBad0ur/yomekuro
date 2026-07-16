import functools
import os

import fire

import mokuro.run as mokuro_run
from mokuro.mokuro_generator import MokuroGenerator

# mokuro's own CLI hardcodes detector_input_size=1024 and text_height=64 with no
# flag to override either. At 1024px a page gets downscaled enough that small
# adjacent elements (e.g. a numbered list's digit markers sitting right next to
# a text line) can merge into one detection box, and OCR then reads the digits
# and the real text as one garbled string. Bumping both trades OCR time for
# quality, which is the point here — same CLI surface as `python -m mokuro`,
# just a different MokuroGenerator default injected via monkeypatch.
#
# detector_input_size is read from an env var (set by converter/mokuro.go, per
# job) rather than fixed, so a reconvert can opt into a higher value from the
# Settings UI for dense scans that still merge lines at the standard size,
# without slowing down every ordinary conversion.
detector_input_size = int(os.environ.get("MOKURO_DETECTOR_INPUT_SIZE", "2048"))
mokuro_run.MokuroGenerator = functools.partial(
    MokuroGenerator, detector_input_size=detector_input_size, text_height=96
)

if __name__ == "__main__":
    fire.Fire(mokuro_run.run)
