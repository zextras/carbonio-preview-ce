# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com
#
# SPDX-License-Identifier: AGPL-3.0-only

import io
import logging
from typing import List, Tuple, Generator, Any

from PIL import ImageDraw, Image, ImageSequence, ImageOps, GifImagePlugin

GifImagePlugin.LOADING_STRATEGY = GifImagePlugin.LoadingStrategy.RGB_ALWAYS

logger: logging.Logger = logging.getLogger(__name__)


def is_img_a_gif(img: Image.Image) -> bool:
    """
    Checks if the given img is a valid GIF
    \f
    :param img: the img to check
    :returns: True if img is a valid GIF
    """
    try:
        # If it's a GIF it's an instance of
        # GifImagePlugin.GifImageFile exposing a .is_animated field
        return img.is_animated
    except AttributeError:
        return False


def parse_to_valid_gif(
    content: io.BytesIO, log: logging.Logger = logger
) -> GifImagePlugin.GifImageFile:
    """
    Parse the bytes buffer in a valid GifImageFile instance.
    Pillow image is a parent class of GifImageFile so there are
    No problems with static checking.
    (underneath it's a GifImageFile instance, but you should type it as image)
     example: img: Image.Image = parse_to_valid_gif(buffer)
    \f
    :param content: the buffer containing the raw bytes of the gif
    :param log: logger to use
    :returns: an instance of GifImageFile containing the gif, if the image was not
    valid it will be replaced by an empty gif
    """
    try:
        gif = GifImagePlugin.GifImageFile(content)
        if gif.is_animated:
            return gif
        else:
            log.debug("File could be loaded as a GIF but it's not animated")
    except SyntaxError as e:
        log.debug(f"Content could not be parsed as a valid GIF file: {e}")
    except AttributeError as e:
        log.debug(
            f"Content was parsed with success (valid image), "
            f"but raised an exception while checking if animated: {e}"
        )
    raise ValueError("Content is not a valid GIF")


def _get_generator_from_gif(gif: Image.Image) -> Generator[Image.Image, Any, None]:
    """
    Given an image it creates a generator, each iteration
    returns an Image object representing each frame of the gif
    \f
    :param gif: gif to iterate over
    :returns: Generator with each instance corresponding to a Image (current frame)
    """
    for frame in ImageSequence.Iterator(gif):
        thumbnail: Image.Image = frame.copy()
        yield thumbnail


def save_gif_to_buffer(
    gif: Image.Image, out_buffer: io.BytesIO, quality: int
) -> io.BytesIO:
    """
    Save a gif to the given buffer and returns the given buffer as well
    :param gif: gif to "render" in the buffer
    :param out_buffer: bytes buffer in which the gif will be saved in
    :param quality: quality value for the gif render
    :returns: bytes buffer containing the rendered gif
    """
    return _save_gif_generator_to_buffer(
        _get_generator_from_gif(gif), out_buffer, gif.info, quality
    )


def _save_gif_generator_to_buffer(
    frames: Generator[Image.Image, Any, None],
    out_buffer: io.BytesIO,
    gif_info: dict,
    quality: int = 60,
) -> io.BytesIO:
    """
    Given a Generator of Image, it saves it into a given bytes buffer
    :param frames: Generator representing all the frames
    :param out_buffer: buffer in which all the images composing a gif will be saved into
    :param gif_info: image info, so that all the frames have the same
    :param quality: quality value for the gif render
    """
    # Save output
    fist_frame = next(frames)  # Handle first frame separately
    fist_frame.info = gif_info  # Copy sequence info
    fist_frame.save(
        out_buffer,
        format="GIF",
        save_all=True,
        quality=quality,
        append_images=list(frames),
    )
    return out_buffer


def __save_img_list_as_gif_to_buffer_deprecated(
    frames: List[Image.Image], out_buff: io.BytesIO, quality: int
) -> io.BytesIO:
    """
    USE save_gif_to_buffer OR _save_gif_generator_to_buffer
    DO NOT USE THIS METHOD UNLESS YOU DO NOT CARE ABOUT PERFORMANCE AND FILE SIZE
    Rationale:
    I decided to keep this method so that no one can use it or recreate it without
    reading my warnings.
    Pillow is notoriously infamous in its handling of gifs. It's not optimized and does
    NOT compress them. Saving like this causes problems, both in performances (testing a
    10mb file takes one hour or so) and in file size (10mb -> 200mb)
    This method may be used but god help you if you only have this option. using the
    iterator is much better!
    """
    if frames:
        frames[0].save(
            out_buff,
            format="GIF",
            save_all=True,
            quality=quality,
            append_images=frames[1:] if len(frames) > 1 else [frames[0]],
        )
        out_buff.seek(0)
    return out_buff


def _resize_gif_frame_by_frame(
    gif: Image.Image, size: Tuple[int, int]
) -> Generator[Image.Image, Any, None]:
    """
    Each frame of the gif will be resized frame by frame
    :param gif: gif to resize
    :param size: desired size of all the frames
    :returns: each frame one at a time resized
    """
    # Get sequence iterator
    for frame in ImageSequence.Iterator(gif):
        # resize does not do side effect on thumbnail, returns a new image
        yield frame.resize(size)


def _crop_gif_frame_by_frame(
    gif: Image.Image, box: Tuple[int, int, int, int]
) -> Generator[Image.Image, Any, None]:
    """
    Each frame of the gif will be cropped frame by frame
    :param gif: gif to crop
    :param box: desired crop of all the frames
    :returns: each frame one at a time cropped
    """
    # Get sequence iterator
    for frame in ImageSequence.Iterator(gif):
        # crop does not do side effect on thumbnail, returns a new image
        yield frame.crop(box)


def _paste_gif_frame_by_frame(
    static_background: Image.Image,
    gif: Image.Image,
    paste_coordinates_box: Tuple[int, int],
) -> Generator[Image.Image, Any, None]:
    """
    Each frame of the gif will be pasted in the static background
    :param static_background: it will be used as background for every frame
    :param gif: gif to paste
    :param paste_coordinates_box: desired paste coordinates of all the frames
    :returns: a new gif, in which every frame contains the given background
    with the old image pasted on top of it
    """
    # Get sequence iterator
    for frame in ImageSequence.Iterator(gif):
        thumbnail: Image.Image = frame.copy()
        static_background.paste(thumbnail, paste_coordinates_box)
        yield thumbnail


def _mask_gif_frame_by_frame(
    mask: Image.Image, gif: Image.Image
) -> Generator[Image.Image, Any, None]:
    """
    Each frame of the gif will be masked with the given mask
    :param mask: Image used to mask
    :param gif: gif to resize
    :returns: each frame one at a time masked
    """
    for frame in ImageSequence.Iterator(gif):
        modified_frame: Image.Image = ImageOps.fit(
            frame, mask.size, centering=(0.5, 0.5)
        )
        modified_frame.paste(0, mask=mask)
        yield modified_frame


def resize_gif(gif: Image.Image, size: Tuple[int, int]) -> Image.Image:
    """
    Resizes every frame of the GIF and then returns
    a PIL.GifImagePlugin.GifImageFile object
    :param gif: gif to resize
    :param size: desired size of the gif
    :returns: resized gif as a GifImageFile object (inherits from Image)
    """
    frames = _resize_gif_frame_by_frame(gif=gif, size=size)
    return Image.open(_save_gif_generator_to_buffer(frames, io.BytesIO(), gif.info))


def crop_gif(gif: Image.Image, box: Tuple[int, int, int, int]) -> Image.Image:
    """
    Crops every frame of the GIF and then returns a PIL.GifImagePlugin.GifImageFile object
    :param gif: gif to crop
    :param box: desired box crop of the gif
    :returns: cropped gif as a GifImageFile object (inherits from Image)
    """
    frames = _crop_gif_frame_by_frame(gif=gif, box=box)
    return Image.open(_save_gif_generator_to_buffer(frames, io.BytesIO(), gif.info))


def paste_gif(
    static_background: Image.Image,
    gif: Image.Image,
    paste_coordinates_box: Tuple[int, int],
) -> Image.Image:
    """
    Paste every frame of the GIF and then returns a PIL.GifImagePlugin.GifImageFile object
    :param static_background: the background that will be the base of every frame
    :param gif: gif to paste over the background
    :param paste_coordinates_box: desired paste coordinates
    :returns: gif with new background as a GifImageFile object (inherits from Image)
    """
    frames = _paste_gif_frame_by_frame(static_background, gif, paste_coordinates_box)
    return Image.open(_save_gif_generator_to_buffer(frames, io.BytesIO(), gif.info))


def add_circle_margins_to_gif(gif: Image.Image) -> Image.Image:
    """
    Adds circle margins to every frame of the gif and then
     returns a PIL.GifImagePlugin.GifImageFile object
    \f
    :param gif: gif to add circles to
    :return: modified gif as a GifImageFile object (inherits from Image)
    """
    size = gif.size
    mask = Image.new("L", size, 255)
    draw = ImageDraw.Draw(mask)
    draw.ellipse((0, 0) + size, fill=0)

    return Image.open(
        _save_gif_generator_to_buffer(
            _mask_gif_frame_by_frame(mask, gif),
            io.BytesIO(),
            gif_info=gif.info,
            quality=60,
        )
    )
