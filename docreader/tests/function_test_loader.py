import unittest
from collections.abc import Callable


def load_function_tests(
    namespace: dict[str, object], existing: unittest.TestSuite
) -> unittest.TestSuite:
    """Expose zero-argument test functions to unittest discovery."""
    suite = unittest.TestSuite(existing)
    functions: list[tuple[str, Callable[[], None]]] = []
    for name, value in namespace.items():
        if name.startswith("test_") and callable(value):
            functions.append((name, value))
    for name, function in sorted(functions):
        suite.addTest(unittest.FunctionTestCase(function, description=name))
    return suite
