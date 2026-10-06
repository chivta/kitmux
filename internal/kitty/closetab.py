# Prints the keys that kitty, loaded with the config files named on the
# command line, binds to close_tab in a plain window, one kitty.conf key spec
# per line. kitmux runs it with `kitty +runpy` to find the close_tab bindings
# that survive the user's own configuration.
import sys

from kitty.config import load_config
from kitty.key_encoding import functional_key_number_to_name_map

MODIFIERS = ((1, 'shift'), (2, 'alt'), (4, 'ctrl'), (8, 'super'), (16, 'hyper'), (32, 'meta'))
CHARACTER_NAMES = {' ': 'space', '+': 'plus'}

opts = load_config(*sys.argv[1:])
for key, definitions in opts.keyboard_modes[''].keymap.items():
    plain = [d for d in definitions if not d.options.when_focus_on and not d.is_sequence]
    if key.is_native or not plain or plain[-1].definition.split()[0] != 'close_tab':
        continue
    name = functional_key_number_to_name_map.get(key.key)
    if name:
        name = name.lower()
    else:
        name = CHARACTER_NAMES.get(chr(key.key), chr(key.key))
    print('+'.join([mod for bit, mod in MODIFIERS if key.mods & bit] + [name]))
