# lounge-screens-manager
Tool which makes it possible to manage content displayed on our lounge screens

## Folder structure
### Top Level
- **standard:** Contains the media files which will be played if no other one-off or weekly event is matching. Does not contain any sub-folders.
- **weekly:** Contains weekly events which should trigger every week at a specific time. Contains a sub-folder for each weekday.
- **once:** Contains one-off events which should only trigger once. Contains a sub-folder for each date of a one-off event.

### Standard
Simply place the media files in the appropriate format in the folder.

### Weekly
To create a weekly event, simply change into the appropriate sub-folder for the weekday and create a folder with the desired time-range.
You can also add a comment to describe the event.
E.g. '09-12 breakfeast' (without the quotes).
Leading zeroes are required for numbers below 10 (01...09).
Only full hours are supported, time ranges such as 19:15-20:45 won't work, go for 19-21 instead.

### Once
To create a one-off event, first create a sub-folder with the desired date and give it a title.
E.g. '2022-09-22 prayer house' (without the quotes).
Leading zeroes are mandatory, as explained in the weekly section.
Inside the date's folder, add a folder for the desired time-range, e.g. '19-22 prayer house'.
Only full hours are supported, time ranges such as '19:15-20:45' won't work, go for '19-21' instead.

### Priority
One-off events are prioritized over weekly events, in case there would be two events at the same time.
The application only checks if it needs to switch the active event once after every event queue has finished.

## File structure
Media files need to follow a set naming structure, to be put into the event queue.
To ensure a set order, every file name needs to start with a number (00...99, leading zeroes are mandatory for numbers under 10).
Afterwards the type needs to be defined (bild, banner-bild, video, banner-video).

### (Banner) Bild
Bild defines a picture which should be displayed on one monitor and not span all three.
The application will convert it to be displayed by all three monitors simultaneously, which will take some time.
Before the conversion is completed, the image will only be displayed on the center monitor.
Bild files should follow the following naming convention:
*'{order number}-Bild-{display length in seconds}sek {title}.{file extension}'*. E.g. '01-bild-120sek test.jpg'.

Banner Bild follows the same convention, the only difference being, that it is meant to be displayed spanning all three monitors.
E.g. E.g. '02-banner-bild-120sek test.jpg'.

### (Banner Video)
Video behaves the same as Bild, however it does not require a display length.
E.g. '03-video test.mp4' for videos meant to be displayed on a singular screen and '04-banner-video test.mp4' for videos which should span across all three screens.
The application will render it to be displayed on all three screens in the background.
Until it is finished processing, the video will only be shown on the center monitor.

## Fixing errors / replacing files
If there is an error in one of the files, or you want to replace an image, delete the old file and upload the fixed file USING A DIFFERENT NAME.
Otherwise, the fixed version might not get synchronized.