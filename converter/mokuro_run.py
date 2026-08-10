import asyncio
import os
import sys

import cv2
import fire
import numpy as np
import torch
from PIL import Image

import mokuro.mokuro_generator as mokuro_generator_mod
import mokuro.run as mokuro_run
from mokuro.cache import cache as mokuro_cache
from mokuro.utils import imread

from comic_text_detector.basemodel import TextDetBase
from comic_text_detector.inference import preprocess_img
from comic_text_detector.utils.db_utils import SegDetectorRepresenter

sys.path.insert(0, "/mit")
from manga_translator.textline_merge import dispatch as merge_dispatch  # noqa: E402
from manga_translator.utils import Quadrilateral  # noqa: E402

# mokuro's own detector post-processing groups raw detections into blocks with
# YOLO + plain IoU-based NMS — structurally blind to two failure modes hit on
# real scans: (1) a spurious extra detection whose box mostly *contains* a
# real one isn't caught, since IoU stays low for very differently-sized boxes,
# so it renders as a garbled duplicate overlapping real text; (2) a 2-line
# block has too few lines for any reliable per-block font-size reference, so
# an OCR-miscounted line's size can't be corrected.
#
# manga-image-translator (a much more actively developed fork built on the
# same comic-text-detector model — see the converter/manga-image-translator
# submodule) dropped that YOLO-box grouping entirely: the detector's
# segmentation branch alone gives individual text-line polygons, and a
# separate graph-based merge (pairwise distance/font-size/angle compatibility,
# then an MST-based outlier split so a chain of transitively-close lines
# doesn't over-merge) groups them into blocks instead. That merge has no
# notion of one detection "containing" another, so the duplicate/nested-box
# failure mode doesn't arise, and blocks aren't limited to what a single
# detector box happened to capture, so a real 2-line dialogue can end up in a
# bigger group with a trustworthy size reference. GraphMangaPageOcr below
# replaces mokuro's per-page detect+group step with this pipeline; the OCR
# model itself (manga-ocr) and everything downstream in converter/epub.go are
# unchanged — the JSON schema this produces is identical to mokuro's own.
detector_input_size = int(os.environ.get("MOKURO_DETECTOR_INPUT_SIZE", "2048"))
box_thresh = 0.6


class GraphMangaPageOcr:
    def __init__(
        self,
        pretrained_model_name_or_path="kha-white/manga-ocr-base",
        force_cpu=False,
        disable_ocr=False,
        **kwargs,
    ):
        self.disable_ocr = disable_ocr
        if self.disable_ocr:
            return
        cuda = torch.cuda.is_available()
        self.device = "cuda" if cuda and not force_cpu else "cpu"
        self.net = TextDetBase(str(mokuro_cache.comic_text_detector), device=self.device, act="leaky")
        self.net.to(self.device)
        self.seg_rep = SegDetectorRepresenter(thresh=0.3)
        from manga_ocr import MangaOcr

        self.mocr = MangaOcr(pretrained_model_name_or_path, force_cpu)

    def _detect(self, img):
        im_h, im_w = img.shape[:2]
        img_in, ratio, dw, dh = preprocess_img(
            img, input_size=(detector_input_size, detector_input_size), device=self.device, to_tensor=True
        )
        with torch.no_grad():
            _, mask, lines_map = self.net(img_in)
        lines, scores = self.seg_rep((detector_input_size, detector_input_size), lines_map)
        idx = np.where(scores[0] > box_thresh)
        lines, scores = lines[0][idx], scores[0][idx]
        if lines.size == 0:
            return np.empty((0, 4, 2), dtype=np.int32)
        resize_ratio = (im_w / (detector_input_size - dw), im_h / (detector_input_size - dh))
        lines = lines.astype(np.float64)
        lines[..., 0] *= resize_ratio[0]
        lines[..., 1] *= resize_ratio[1]
        return lines.astype(np.int32)

    def _ocr_quad(self, img, pts):
        im_h, im_w = img.shape[:2]
        x0 = max(0, int(pts[:, 0].min()) - 2)
        y0 = max(0, int(pts[:, 1].min()) - 2)
        x1 = min(im_w, int(pts[:, 0].max()) + 2)
        y1 = min(im_h, int(pts[:, 1].max()) + 2)
        if x1 <= x0 or y1 <= y0:
            return ""
        crop = cv2.cvtColor(img[y0:y1, x0:x1], cv2.COLOR_BGR2RGB)
        return self.mocr(Image.fromarray(crop))

    def __call__(self, img_path):
        img = imread(img_path)
        im_h, im_w = img.shape[:2]
        result = {"version": "mokuro_run-graph-merge", "img_width": im_w, "img_height": im_h, "blocks": []}
        if self.disable_ocr:
            return result

        quads = []
        for pts in self._detect(img):
            text = self._ocr_quad(img, pts)
            if not text.strip():
                continue
            quads.append(Quadrilateral(pts.astype(np.float64), text, 1.0))

        regions = asyncio.run(merge_dispatch(quads, im_w, im_h))
        for region in regions:
            lines_coords = region.lines.tolist()
            if not lines_coords:
                continue
            xs = [p[0] for ln in lines_coords for p in ln]
            ys = [p[1] for ln in lines_coords for p in ln]
            result["blocks"].append(
                {
                    "box": [int(min(xs)), int(min(ys)), int(max(xs)), int(max(ys))],
                    "vertical": region.direction in ("v", "vr"),
                    "font_size": float(region.font_size),
                    "lines_coords": lines_coords,
                    "lines": list(region.texts),
                }
            )
        return result


# See the block comment above for why this replaces mokuro's own detect+group
# step rather than just tuning its parameters. Patched on mokuro.mokuro_generator
# (not mokuro.manga_page_ocr, the class's origin module) because that's the
# module namespace MokuroGenerator.init_models() actually resolves the name
# from at call time — the same reasoning that made the pre-existing
# MokuroGenerator patch here target mokuro_run.MokuroGenerator (mokuro.run's
# own binding) rather than mokuro.mokuro_generator.MokuroGenerator.
mokuro_generator_mod.MangaPageOcr = GraphMangaPageOcr

if __name__ == "__main__":
    fire.Fire(mokuro_run.run)
