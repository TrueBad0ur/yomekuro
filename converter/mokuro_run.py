import functools

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
mokuro_run.MokuroGenerator = functools.partial(
    MokuroGenerator, detector_input_size=2048, text_height=96
)

if __name__ == "__main__":
    fire.Fire(mokuro_run.run)
