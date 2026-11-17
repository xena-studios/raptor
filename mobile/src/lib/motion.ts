import * as Haptics from "expo-haptics";
import { Platform } from "react-native";
import { Easing, FadeIn, FadeOut, LinearTransition } from "react-native-reanimated";

// Motion in the app says what changed and nothing more: presses answer at
// once, things that arrive fade in, things that move slide, and only what's
// waiting (a server starting, a placeholder) keeps moving. Quick, eased out
// (no bounce), and off when the phone's Reduce Motion is on: Reanimated
// follows that setting for every animation here.

// Ease-out quart: fast at first, settling gently.
export const easeOut = Easing.bezier(0.25, 1, 0.5, 1);

export const duration = {
  press: 90,
  release: 220,
  enter: 220,
  exit: 160,
  move: 260,
  pulse: 900,
};

// arrive is how something new shows up: a fade, so a list filling in
// doesn't jump; leave is the quicker way out.
export const arrive = FadeIn.duration(duration.enter).easing(easeOut);
export const leave = FadeOut.duration(duration.exit).easing(easeOut);
// settle animates a view to its new size or place when what's above or
// inside it changes.
export const settle = LinearTransition.duration(duration.move).easing(easeOut);

// Haptics: a light tap for buttons, a firmer one for something that
// changes a server, and the system's success and error for outcomes.
const ios = Platform.OS === "ios";
const quiet = () => {};

export const haptic = {
  tap: () => {
    if (ios) Haptics.selectionAsync().catch(quiet);
  },
  press: () => Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Light).catch(quiet),
  heavy: () => Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Medium).catch(quiet),
  success: () => Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success).catch(quiet),
  error: () => Haptics.notificationAsync(Haptics.NotificationFeedbackType.Error).catch(quiet),
};
